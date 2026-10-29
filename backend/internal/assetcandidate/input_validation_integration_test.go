package assetcandidate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/asset"
	"netassets/internal/auth"
)

// Own fixtures only: do not truncate a retained database or rely on another test's rows.
func candidateFieldFixture(t *testing.T) (context.Context, *pgxpool.Pool, *Service, auth.Principal, string, string) {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	var org, user string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'候选字段测试集团','group') RETURNING id::text`, "CAND_FIELDS_"+suffix).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','候选字段测试员') RETURNING id::text`, "cand_fields_"+suffix).Scan(&user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM asset_candidate_followups WHERE actor_user_id=$1::uuid`, `DELETE FROM audit_logs WHERE user_id=$1::uuid`, `DELETE FROM asset_candidates WHERE created_by=$1::uuid`, `DELETE FROM users WHERE id=$1::uuid`} {
			if _, err := pool.Exec(ctx, q, user); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
		if _, err := pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, org); err != nil {
			t.Errorf("organization cleanup: %v", err)
		}
	})
	p := auth.Principal{UserID: user, Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{"asset.manage"}}
	return ctx, pool, NewService(pool, asset.NewService(pool)), p, org, suffix
}

func TestCandidateUnicodeFieldsPersistenceAndRejections(t *testing.T) {
	ctx, pool, s, p, org, suffix := candidateFieldFixture(t)
	snapshot := func(t *testing.T) [5]int {
		t.Helper()
		var n [5]int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM asset_candidates WHERE created_by=$1::uuid),(SELECT count(*) FROM audit_logs WHERE user_id=$1::uuid),(SELECT count(*) FROM asset_candidate_followups WHERE actor_user_id=$1::uuid),(SELECT count(*) FROM assets WHERE created_by=$1::uuid),(SELECT count(*) FROM inventory_asset_drafts WHERE organization_id=$2::uuid)`, p.UserID, org).Scan(&n[0], &n[1], &n[2], &n[3], &n[4]); err != nil {
			t.Fatal(err)
		}
		return n
	}
	base := func(key string) CreateInput {
		return CreateInput{AssetType: "domain", AssetValue: key + "-" + suffix + ".example.com", OrganizationID: org, Source: "manual"}
	}
	for i, unit := range []string{"a", "中", "🙂"} {
		t.Run(fmt.Sprintf("valid-%d", i), func(t *testing.T) {
			in := base(fmt.Sprintf("valid-%d", i))
			in.Name = strings.Repeat(unit, 255)
			in.SourceReference = strings.Repeat(unit, 255)
			in.Notes = strings.Repeat(unit, 5000)
			before := snapshot(t)
			item, err := s.Create(ctx, p, in, "candidate-fields-create", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, p, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != in.Name || got.SourceReference != in.SourceReference || got.SuggestedData["notes"] != in.Notes {
				t.Fatal("candidate unicode input was truncated or changed")
			}
			after := snapshot(t)
			if after != [5]int{before[0] + 1, before[1] + 1, before[2], before[3], before[4]} {
				t.Fatalf("create side effects: %v -> %v", before, after)
			}
			for _, tc := range []struct {
				in      FollowUpInput
				message string
			}{
				{FollowUpInput{Action: "bad", Comment: "test"}, "请选择有效的跟进类型"},
				{FollowUpInput{Comment: "　 "}, "请填写跟进说明"},
				{FollowUpInput{Comment: strings.Repeat(unit, 4001)}, "跟进说明最多 4000 字"},
			} {
				before := snapshot(t)
				_, err := s.AddFollowUp(ctx, p, item.ID, tc.in, "invalid-followup", "127.0.0.1")
				if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
					t.Fatalf("follow-up error: %v", err)
				}
				if after := snapshot(t); after != before {
					t.Fatalf("invalid follow-up wrote: %v -> %v", before, after)
				}
			}
			comment := strings.Repeat(unit, 4000)
			follow, err := s.AddFollowUp(ctx, p, item.ID, FollowUpInput{Comment: comment}, "candidate-fields-followup", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			var stored string
			var details []byte
			if err := pool.QueryRow(ctx, `SELECT comment FROM asset_candidate_followups WHERE id=$1::uuid`, follow.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, `SELECT details FROM audit_logs WHERE user_id=$1::uuid AND resource_id=$2 AND action='asset_candidate.follow_up'`, p.UserID, item.ID).Scan(&details); err != nil {
				t.Fatal(err)
			}
			var audit map[string]any
			if err := json.Unmarshal(details, &audit); err != nil {
				t.Fatal(err)
			}
			if follow.Action != "note" || stored != comment || audit["comment"] != comment {
				t.Fatal("follow-up or audit lost Unicode content")
			}
		})
	}
	for _, unit := range []string{"a", "中", "🙂"} {
		for _, field := range []string{"name", "source_reference", "notes"} {
			t.Run("reject-"+field+unit, func(t *testing.T) {
				in := base("invalid")
				message := ""
				switch field {
				case "name":
					in.Name = strings.Repeat(unit, 256)
					message = "资产名称最多 255 字"
				case "source_reference":
					in.SourceReference = strings.Repeat(unit, 256)
					message = "来源参考最多 255 字"
				case "notes":
					in.Notes = strings.Repeat(unit, 5001)
					message = "备注最多 5000 字"
				}
				before := snapshot(t)
				_, err := s.Create(ctx, p, in, "invalid-candidate", "127.0.0.1")
				if !errors.Is(err, ErrInvalid) || err.Error() != message {
					t.Fatalf("create error: %v", err)
				}
				if after := snapshot(t); after != before {
					t.Fatalf("invalid create wrote: %v -> %v", before, after)
				}
			})
		}
	}
	before := snapshot(t)
	if _, err := pool.Exec(ctx, `UPDATE organizations SET status='disabled' WHERE id=$1::uuid`, org); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create(ctx, p, base("disabled"), "disabled-candidate", "127.0.0.1")
	if !errors.Is(err, ErrInvalid) || err.Error() != "建议所属组织已停用，请重新选择" {
		t.Fatalf("disabled org: %v", err)
	}
	if after := snapshot(t); after != before {
		t.Fatalf("disabled create wrote: %v -> %v", before, after)
	}
}

func TestCandidateInvalidOrganizationIsPublicFieldError(t *testing.T) {
	ctx, _, s, p, _, suffix := candidateFieldFixture(t)
	_, err := s.Create(ctx, p, CreateInput{AssetType: "domain", AssetValue: "uuid-" + suffix + ".example.com", OrganizationID: "not-a-uuid"}, "invalid-uuid", "127.0.0.1")
	if !errors.Is(err, ErrInvalid) || err.Error() != "请选择有效的建议所属组织" {
		t.Fatalf("organization field error: %v", err)
	}
}

func TestUnassignedFindingCandidateCanBeReviewed(t *testing.T) {
	ctx, pool, s, p, _, suffix := candidateFieldFixture(t)
	for _, operation := range []string{"ignore", "invalidate"} {
		t.Run(operation, func(t *testing.T) {
			item, err := s.Create(ctx, p, CreateInput{AssetType: "domain", AssetValue: operation + "-" + suffix + ".example.com", Source: "finding_import"}, "unassigned-create", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			note := "核实无归属线索"
			want := "ignored"
			if operation == "ignore" {
				err = s.Ignore(ctx, p, item.ID, ReviewInput{ReviewNote: note}, "unassigned-review", "127.0.0.1")
			} else {
				want = "invalid"
				err = s.Invalidate(ctx, p, item.ID, ReviewInput{ReviewNote: note}, "unassigned-review", "127.0.0.1")
			}
			if err != nil {
				t.Fatalf("review unassigned candidate: %v", err)
			}
			got, err := s.Get(ctx, p, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != want || got.ReviewNote != note || got.OrganizationID != "" {
				t.Fatalf("review did not persist: %+v", got)
			}
			var audits int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_id=$1 AND action=$2 AND organization_id IS NULL`, item.ID, "asset_candidate."+operation).Scan(&audits); err != nil {
				t.Fatal(err)
			}
			if audits != 1 {
				t.Fatalf("expected single null-org review audit, got %d", audits)
			}
		})
	}
}

func TestCandidateAcceptanceFieldMessagesDoNotWrite(t *testing.T) {
	ctx, pool, s, p, org, suffix := candidateFieldFixture(t)
	item, err := s.Create(ctx, p, CreateInput{AssetType: "domain", AssetValue: "accept-" + suffix + ".example.com", OrganizationID: org}, "accept-fields-create", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func(t *testing.T) [3]int {
		t.Helper()
		var n [3]int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_logs WHERE user_id=$1::uuid),(SELECT count(*) FROM inventory_asset_drafts WHERE organization_id=$2::uuid),(SELECT count(*) FROM assets WHERE created_by=$1::uuid)`, p.UserID, org).Scan(&n[0], &n[1], &n[2]); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, tc := range []struct {
		name, message string
		change        func(*AcceptInput)
	}{
		{"environment", "请选择有效的部署环境", func(in *AcceptInput) { in.Environment = "bad" }},
		{"network_scope", "请选择有效的网络范围", func(in *AcceptInput) { in.NetworkScope = "bad" }},
		{"status", "请选择有效的资产状态", func(in *AcceptInput) { in.Status = "bad" }},
		{"importance", "请选择有效的资产重要程度", func(in *AcceptInput) { in.Importance = "bad" }},
		{"name", "资产名称最多 255 字", func(in *AcceptInput) { in.Name = strings.Repeat("🙂", 256) }},
		{"owner name", "负责人最多 128 字", func(in *AcceptInput) { in.OwnerName = strings.Repeat("中", 129) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := snapshot(t)
			in := AcceptInput{CreateInput: asset.CreateInput{Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal"}}
			tc.change(&in)
			_, err := s.Accept(ctx, p, item.ID, in, "accept-invalid", "127.0.0.1")
			if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
				t.Fatalf("single accept: %v", err)
			}
			batch := s.BatchAccept(ctx, p, BatchAcceptInput{Items: []BatchAcceptItem{{CandidateID: item.ID, Input: in}}}, "batch-invalid", "127.0.0.1")
			if batch.Failed != 1 || batch.Succeeded != 0 || len(batch.Items) != 1 || batch.Items[0].Code != "invalid_asset_candidate" || batch.Items[0].Message != tc.message {
				t.Fatalf("batch accept: %+v", batch)
			}
			_, err = s.Accept(ctx, auth.Principal{}, item.ID, in, "accept-forbidden", "127.0.0.1")
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("accept permission order: %v", err)
			}
			if after := snapshot(t); after != before {
				t.Fatalf("rejected acceptance wrote: %v -> %v", before, after)
			}
			got, err := s.Get(ctx, p, item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "pending" || got.WorkingVersionDraftID != "" {
				t.Fatal("rejected acceptance changed candidate")
			}
		})
	}
}
