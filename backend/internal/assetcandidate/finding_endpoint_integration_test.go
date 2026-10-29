package assetcandidate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/asset"
	"netassets/internal/auth"
)

// A scanner's service discovery is identified by host AND port. Only an IP
// discovery may collapse host:port to its address during confirmation.
func TestFindingEndpointConfirmationPreservesServiceIdentity(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("requires dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	var orgID, userID string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'端点确认测试集团','group') RETURNING id::text`, "ENDPOINT_"+suffix).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','端点确认测试员') RETURNING id::text`, "endpoint_"+suffix).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{UserID: userID, Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{"asset.manage"}}
	s := NewService(pool, asset.NewService(pool))
	for _, tc := range []struct {
		name, kind, value, want string
		portNote                bool
	}{
		{"service-ip", "service", "192.0.2.17:1521", "192.0.2.17:1521", false},
		{"service-host", "service", "listener-" + suffix + ".example.com:8443", "listener-" + suffix + ".example.com:8443", false},
		{"ip-endpoint", "ip", "192.0.2.18:1521", "192.0.2.18", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Make the fixture's IP identities unique even when this test is rerun on a
			// retained database. Source data and earlier failure evidence are not reset.
			if strings.HasPrefix(tc.value, "192.0.2.") {
				host := fmt.Sprintf("2001:db8:%x::%s", time.Now().UnixNano()&0xffff, map[bool]string{true: "18", false: "17"}[tc.portNote])
				tc.value = "[" + host + "]:1521"
				tc.want = tc.value
				if tc.portNote {
					tc.want = host
				}
			}
			candidate, err := s.Create(ctx, p, CreateInput{AssetType: tc.kind, AssetValue: tc.value, OrganizationID: orgID, Source: "finding_import"}, "endpoint-create", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			created, err := s.Accept(ctx, p, candidate.ID, AcceptInput{CreateInput: asset.CreateInput{Name: "漏洞端点", Notes: "原始说明"}}, "endpoint-accept", "127.0.0.1")
			if err != nil {
				t.Fatalf("confirm %s %s: %v", tc.kind, tc.value, err)
			}
			if created.Type != tc.kind || created.Value != tc.want || !created.IsWorkingCopy {
				t.Fatalf("unexpected working version: type=%s value=%s working=%t", created.Type, created.Value, created.IsWorkingCopy)
			}
			if strings.Contains(created.Notes, "漏洞导入记录端口 1521") != tc.portNote {
				t.Fatalf("port evidence mismatch: %q", created.Notes)
			}
			if !strings.Contains(created.Notes, "原始说明") {
				t.Fatal("existing notes lost")
			}
			var status, draftID string
			if err := pool.QueryRow(ctx, `SELECT status,working_version_draft_id::text FROM asset_candidates WHERE id=$1::uuid`, candidate.ID).Scan(&status, &draftID); err != nil {
				t.Fatal(err)
			}
			if status != "pending" || draftID != created.ID {
				t.Fatalf("candidate working version not linked atomically: %s %s", status, draftID)
			}
		})
	}
}
