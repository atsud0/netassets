package inventorytask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/asset"
	"netassets/internal/auth"
)

// Use actual reference queries in a rolled-back transaction. Invalid references
// must not reveal whether an object exists in another organization.
func TestDraftReferenceValidationMessages(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	id := func(query string, args ...any) string {
		t.Helper()
		var result string
		if err := tx.QueryRow(ctx, query, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	org := id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES('DRAFT_REF_ORG','草稿引用校验','subsidiary') RETURNING id::text`)
	otherOrg := id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES('DRAFT_REF_OTHER','非所属组织','subsidiary') RETURNING id::text`)
	disabledOrg := id(`INSERT INTO organizations(organization_code,organization_name,organization_type,status) VALUES('DRAFT_REF_DISABLED','已停用组织','subsidiary','disabled') RETURNING id::text`)
	user := id(`INSERT INTO users(username,password_hash,display_name) VALUES('draft_ref_user','unused','草稿引用用户') RETURNING id::text`)
	if _, err := tx.Exec(ctx, `INSERT INTO user_organizations(user_id,organization_id) VALUES($1::uuid,$2::uuid)`, user, org); err != nil {
		t.Fatal(err)
	}
	area := id(`INSERT INTO asset_network_areas(organization_id,area_name) VALUES($1::uuid,'草稿引用区域') RETURNING id::text`, org)
	system := id(`INSERT INTO business_systems(system_code,system_name,owner_organization_id,system_status,created_by) VALUES('DRAFT_REF_SYS','草稿引用系统',$1::uuid,'running',$2::uuid) RETURNING id::text`, org, user)
	draft := id(`INSERT INTO inventory_business_system_drafts(organization_id,operation,system_code,system_name,system_status,created_by,updated_by) VALUES($1::uuid,'create','DRAFT_REF_NEW','草稿引用新系统','running',$2::uuid,$2::uuid) RETURNING id::text`, org, user)
	missing := "00000000-0000-0000-0000-000000000000"
	assertMessage := func(t *testing.T, err error, message string) {
		t.Helper()
		var typed *InputValidationError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &typed) || typed.Message != message {
			t.Fatalf("error=%v, want typed %q", err, message)
		}
	}
	t.Run("valid references", func(t *testing.T) {
		for _, systemID := range []string{system, draft, ""} {
			if err := validateDraftReferences(ctx, tx, org, asset.CreateInput{NetworkAreaID: area, BusinessSystemID: systemID, OwnerUserID: user}); err != nil {
				t.Fatal(err)
			}
			formalID, draftID, err := resolveBusinessSystemReference(ctx, tx, org, " "+systemID+" ")
			if err != nil {
				t.Fatal(err)
			}
			if (systemID == system && (formalID != system || draftID != "")) || (systemID == draft && (formalID != "" || draftID != draft)) || (systemID == "" && (formalID != "" || draftID != "")) {
				t.Fatalf("unexpected resolution %q %q", formalID, draftID)
			}
		}
	})
	t.Run("disabled organization", func(t *testing.T) {
		assertMessage(t, validateDraftReferences(ctx, tx, disabledOrg, asset.CreateInput{}), "所属组织已停用，请联系管理员")
		assertMessage(t, validateBusinessSystemDraftOrganization(ctx, tx, disabledOrg), "所属组织已停用，请联系管理员")
	})
	t.Run("missing organization retains not found", func(t *testing.T) {
		for _, err := range []error{validateDraftReferences(ctx, tx, missing, asset.CreateInput{}), validateBusinessSystemDraftOrganization(ctx, tx, missing)} {
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("error=%v", err)
			}
		}
	})
	for _, tc := range []struct {
		name    string
		input   asset.CreateInput
		message string
	}{
		{"missing area", asset.CreateInput{NetworkAreaID: missing}, "请选择本组织有效且已启用的网络区域"},
		{"other organization area", asset.CreateInput{NetworkAreaID: area}, "请选择本组织有效且已启用的网络区域"},
		{"missing system", asset.CreateInput{BusinessSystemID: missing}, "请选择本组织有效的业务系统或填报草稿"},
		{"other organization system", asset.CreateInput{BusinessSystemID: system}, "请选择本组织有效的业务系统或填报草稿"},
		{"other organization system draft", asset.CreateInput{BusinessSystemID: draft}, "请选择本组织有效的业务系统或填报草稿"},
		{"missing owner", asset.CreateInput{OwnerUserID: missing}, "请选择本组织有效且已启用的负责人"},
		{"other organization owner", asset.CreateInput{OwnerUserID: user}, "请选择本组织有效且已启用的负责人"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targetOrg := org
			if strings.HasPrefix(tc.name, "other organization") {
				targetOrg = otherOrg
			}
			assertMessage(t, validateDraftReferences(ctx, tx, targetOrg, tc.input), tc.message)
		})
	}
	for _, reference := range []string{missing, system, draft} {
		t.Run("unavailable system resolution/"+reference, func(t *testing.T) {
			_, _, err := resolveBusinessSystemReference(ctx, tx, otherOrg, reference)
			assertMessage(t, err, "请选择本组织有效的业务系统或填报草稿")
		})
	}
	if _, err := tx.Exec(ctx, `UPDATE asset_network_areas SET status='disabled' WHERE id=$1::uuid;`, area); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET status='disabled' WHERE id=$1::uuid`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE business_systems SET deleted_at=now() WHERE id=$1::uuid`, system); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE inventory_business_system_drafts SET operation='delete' WHERE id=$1::uuid`, draft); err != nil {
		t.Fatal(err)
	}
	t.Run("disabled area", func(t *testing.T) {
		assertMessage(t, validateDraftReferences(ctx, tx, org, asset.CreateInput{NetworkAreaID: area}), "请选择本组织有效且已启用的网络区域")
	})
	t.Run("disabled owner", func(t *testing.T) {
		assertMessage(t, validateDraftReferences(ctx, tx, org, asset.CreateInput{OwnerUserID: user}), "请选择本组织有效且已启用的负责人")
	})
	for _, reference := range []string{system, draft} {
		t.Run("deleted system/"+reference, func(t *testing.T) {
			assertMessage(t, validateDraftReferences(ctx, tx, org, asset.CreateInput{BusinessSystemID: reference}), "请选择本组织有效的业务系统或填报草稿")
			_, _, err := resolveBusinessSystemReference(ctx, tx, org, reference)
			assertMessage(t, err, "请选择本组织有效的业务系统或填报草稿")
		})
	}
}

func TestDraftVersionValidationMessages(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := NewService(pool)
	permitted := auth.Principal{Permissions: []string{"inventory.manage", "asset.manage", "system.manage"}}
	for _, version := range []int{-1, 0} {
		for _, operation := range []string{"create asset", "update asset", "delete asset", "update system", "delete system"} {
			t.Run(fmt.Sprintf("%s/%d", operation, version), func(t *testing.T) {
				var err error
				switch operation {
				case "create asset":
					_, err = svc.CreateAssetDraft(ctx, permitted, "task", "org", asset.CreateInput{ExpectedVersion: &version}, "", "")
				case "update asset":
					_, err = svc.UpdateAssetDraft(ctx, permitted, "task", "org", "draft", asset.CreateInput{ExpectedVersion: &version}, "", "")
				case "delete asset":
					err = svc.DeleteAssetDraft(ctx, permitted, "task", "org", "draft", version, "", "")
				case "update system":
					_, err = svc.UpdateBusinessSystemDraft(ctx, permitted, "task", "org", "draft", BusinessSystemDraftInput{ExpectedVersion: &version}, "", "")
				case "delete system":
					err = svc.DeleteBusinessSystemDraft(ctx, permitted, "task", "org", "draft", version, "", "")
				}
				var typed *InputValidationError
				if !errors.Is(err, ErrInvalid) || !errors.As(err, &typed) || typed.Message != "草稿版本无效，请刷新后重试" {
					t.Fatalf("error=%v", err)
				}
			})
		}
	}
	for _, kind := range []string{"asset", "system"} {
		t.Run("missing version/"+kind, func(t *testing.T) {
			var err error
			if kind == "asset" {
				_, err = svc.UpdateAssetDraft(ctx, permitted, "task", "org", "draft", asset.CreateInput{}, "", "")
			} else {
				_, err = svc.UpdateBusinessSystemDraft(ctx, permitted, "task", "org", "draft", BusinessSystemDraftInput{}, "", "")
			}
			var typed *InputValidationError
			if !errors.As(err, &typed) || typed.Message != "草稿版本无效，请刷新后重试" {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
