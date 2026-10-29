package businesssystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
)

func TestBusinessSystemOptionalFieldBoundaries(t *testing.T) {
	for _, unit := range []string{"a", "中", "🙂"} {
		for _, tc := range []struct {
			name, message string
			limit         int
			set           func(*CreateInput, string)
		}{
			{"short name", "系统简称最多 128 字", 128, func(in *CreateInput, s string) { in.ShortName = s }},
			{"owner", "负责人最多 128 字", 128, func(in *CreateInput, s string) { in.OwnerName = s }},
			{"deployment", "部署方式最多 32 字", 32, func(in *CreateInput, s string) { in.DeploymentType = s }},
		} {
			t.Run(unit+"/"+tc.name, func(t *testing.T) {
				in := validBusinessSystemInput()
				tc.set(&in, strings.Repeat(unit, tc.limit))
				if err := PrepareCreateInput(&in); err != nil {
					t.Fatalf("full limit: %v", err)
				}
				tc.set(&in, strings.Repeat(unit, tc.limit+1))
				err := PrepareCreateInput(&in)
				var typed *InputValidationError
				if !errors.Is(err, ErrInvalid) || !errors.As(err, &typed) || typed.Message != tc.message {
					t.Fatalf("error=%v, want %q", err, tc.message)
				}
			})
		}
	}
}

func TestBusinessSystemOptionalFieldsDoNotMutateOnRejection(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires dedicated migrated database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	stamp := fmt.Sprint(time.Now().UnixNano())
	var user, org, role string
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, q := range []string{`DELETE FROM inventory_business_system_drafts WHERE created_by=$1::uuid`, `DELETE FROM business_systems WHERE created_by=$1::uuid`, `DELETE FROM notification_events WHERE actor_user_id=$1::uuid`, `DELETE FROM audit_logs WHERE user_id=$1::uuid`, `DELETE FROM users WHERE id=$1::uuid`} {
			if user != "" {
				if _, err := pool.Exec(ctx, q, user); err != nil {
					t.Errorf("cleanup: %v", err)
				}
			}
		}
		if role != "" {
			if _, err := pool.Exec(ctx, `DELETE FROM roles WHERE id=$1::uuid`, role); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
		if org != "" {
			if _, err := pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, org); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	}()
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'系统可选字段测试','group') RETURNING id::text`, "BS_FIELDS_"+stamp).Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','系统字段测试') RETURNING id::text`, "bs_fields_"+stamp).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO roles(role_key,role_name,role_scope,is_system,status) VALUES($1,'系统字段夹具角色','organization',false,'enabled') RETURNING id::text`, "bs_fields_"+stamp).Scan(&role); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO permissions(permission_key,permission_name) VALUES('system.manage','管理系统信息') ON CONFLICT DO NOTHING`)
	exec(`INSERT INTO role_permissions(role_id,permission_key) VALUES($1::uuid,'system.manage')`, role)
	exec(`INSERT INTO user_roles(user_id,role_id,organization_id,data_scope) VALUES($1::uuid,$2::uuid,$3::uuid,'organization')`, user, role, org)
	principal := auth.Principal{UserID: user, Roles: []string{"group_admin"}, Permissions: []string{"system.manage"}}
	svc := NewService(pool)
	snapshot := func() string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_object('systems',(SELECT jsonb_agg(b ORDER BY id) FROM business_systems b WHERE created_by=$1::uuid),'drafts',(SELECT jsonb_agg(d ORDER BY id) FROM inventory_business_system_drafts d WHERE created_by=$1::uuid),'audits',(SELECT jsonb_agg(a ORDER BY id) FROM audit_logs a WHERE user_id=$1::uuid),'events',(SELECT jsonb_agg(n ORDER BY id) FROM notification_events n WHERE actor_user_id=$1::uuid))::text`, user).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, mode := range []string{"formal", "working"} {
		if mode == "working" {
			principal.Roles = []string{"organization_admin"}
		}
		for i, unit := range []string{"a", "中", "🙂"} {
			input := validBusinessSystemInput()
			input.OrganizationID = org
			input.Code = fmt.Sprintf("FIELDS_%s_%s_%d", mode, stamp, i)
			input.Name = strings.Repeat(unit, 255)
			input.ShortName = strings.Repeat(unit, 128)
			input.OwnerName = strings.Repeat(unit, 128)
			input.DeploymentType = strings.Repeat(unit, 32)
			created, err := svc.Create(ctx, principal, input, "fields-create", "")
			if err != nil {
				t.Fatal(err)
			}
			assertFull := func(item BusinessSystem) {
				t.Helper()
				if item.Name != input.Name || item.ShortName != input.ShortName || item.OwnerName != input.OwnerName || item.DeploymentType != input.DeploymentType {
					t.Fatal("full-limit fields truncated or changed")
				}
			}
			assertFull(created)
			before := snapshot()
			for _, tc := range []struct {
				name, message string
				change        func(*CreateInput)
			}{
				{"short name", "系统简称最多 128 字", func(in *CreateInput) { in.ShortName += unit }},
				{"owner", "负责人最多 128 字", func(in *CreateInput) { in.OwnerName += unit }},
				{"deployment", "部署方式最多 32 字", func(in *CreateInput) { in.DeploymentType += unit }},
			} {
				for _, method := range []string{"create", "update"} {
					t.Run(mode+"/"+fmt.Sprint(i)+"/"+tc.name+"/"+method, func(t *testing.T) {
						in := input
						in.ExpectedVersion = &created.Version
						tc.change(&in)
						var err error
						if method == "create" {
							in.Code += "_NEW"
							_, err = svc.Create(ctx, principal, in, "fields-rejected", "")
						} else {
							_, err = svc.Update(ctx, principal, created.ID, in, "fields-rejected", "")
						}
						var typed *InputValidationError
						if !errors.Is(err, ErrInvalid) || !errors.As(err, &typed) || typed.Message != tc.message {
							t.Errorf("error=%v, want %q", err, tc.message)
						}
						if snapshot() != before {
							t.Fatal("rejected write changed formal data, CAS, audit or notifications")
						}
					})
				}
			}
			input.ExpectedVersion = &created.Version
			updated, err := svc.Update(ctx, principal, created.ID, input, "fields-valid-update", "")
			if err != nil {
				t.Fatal(err)
			}
			assertFull(updated)
			var name, short, owner, deployment string
			var version int
			table := "business_systems"
			if mode == "working" {
				table = "inventory_business_system_drafts"
			}
			if err := pool.QueryRow(ctx, `SELECT system_name,short_name,owner_name,deployment_type,version FROM `+table+` WHERE id=$1::uuid`, created.ID).Scan(&name, &short, &owner, &deployment, &version); err != nil {
				t.Fatal(err)
			}
			if name != input.Name || short != input.ShortName || owner != input.OwnerName || deployment != input.DeploymentType || version != created.Version+1 {
				t.Fatal("database full Unicode fields or CAS mismatch")
			}
		}
	}
}
