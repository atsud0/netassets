package inventorytask

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/businesssystem"
	"netassets/internal/customfield"
)

func TestInventoryDraftValidationDoesNotMutate(t *testing.T) {
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
	stamp := fmt.Sprint(time.Now().UnixNano())
	var user, org, role, task, assetField, systemField string
	defer func() {
		for _, q := range []string{
			`DELETE FROM inventory_asset_drafts WHERE created_by=$1::uuid`,
			`DELETE FROM inventory_business_system_drafts WHERE created_by=$1::uuid`,
			`DELETE FROM inventory_task_organizations WHERE task_id IN (SELECT id FROM inventory_tasks WHERE created_by=$1::uuid)`,
			`DELETE FROM inventory_tasks WHERE created_by=$1::uuid`,
			`DELETE FROM notification_events WHERE actor_user_id=$1::uuid`,
			`DELETE FROM audit_logs WHERE user_id=$1::uuid`,
			`DELETE FROM users WHERE id=$1::uuid`,
		} {
			if user != "" {
				if _, err := pool.Exec(ctx, q, user); err != nil {
					t.Errorf("clean up own fixture: %v", err)
				}
			}
		}
		for _, fixture := range []struct{ table, id string }{{"custom_field_definitions", assetField}, {"custom_field_definitions", systemField}, {"roles", role}, {"organizations", org}} {
			if fixture.id != "" {
				if _, err := pool.Exec(ctx, `DELETE FROM `+fixture.table+` WHERE id=$1::uuid`, fixture.id); err != nil {
					t.Errorf("clean up own fixture: %v", err)
				}
			}
		}
	}()
	id := func(q string, args ...any) string {
		t.Helper()
		var result string
		if err := pool.QueryRow(ctx, q, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	org = id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'草稿拒绝无副作用','subsidiary') RETURNING id::text`, "DRAFT_VALID_"+stamp)
	user = id(`INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','草稿字段测试') RETURNING id::text`, "draft_valid_"+stamp)
	role = id(`INSERT INTO roles(role_key,role_name,role_scope,is_system) VALUES($1,'草稿字段夹具角色','organization',false) RETURNING id::text`, "draft_valid_"+stamp)
	exec(`INSERT INTO user_roles(user_id,role_id,organization_id,data_scope) VALUES($1::uuid,$2::uuid,$3::uuid,'organization')`, user, role, org)
	task = id(`INSERT INTO inventory_tasks(task_name,start_at,deadline_at,status,created_by,required_fields,asset_network_scope) VALUES('草稿字段校验',now()-interval '1 day',now()+interval '1 day','active',$1::uuid,'["network_assets","business_systems"]'::jsonb,'public') RETURNING id::text`, user)
	exec(`INSERT INTO inventory_task_organizations(task_id,organization_id,status) VALUES($1::uuid,$2::uuid,'in_progress')`, task, org)
	key := "draft_number_" + stamp
	assetField = id(`INSERT INTO custom_field_definitions(resource_type,field_key,field_name,field_type,is_required,is_enabled) VALUES('asset',$1,'草稿数字字段','number',false,true) RETURNING id::text`, key)
	systemField = id(`INSERT INTO custom_field_definitions(resource_type,field_key,field_name,field_type,is_required,is_enabled) VALUES('business_system',$1,'草稿数字字段','number',false,true) RETURNING id::text`, key)
	p := auth.Principal{UserID: user, Roles: []string{"organization_admin"}, Permissions: []string{"inventory.manage", "asset.manage", "system.manage"}}
	svc := NewService(pool)
	assetInput := asset.CreateInput{Type: "domain", Value: "draft-" + stamp + ".example.com", Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal", CustomValues: []customfield.Value{{FieldKey: key, Value: float64(0)}}}
	createdAsset, err := svc.CreateAssetDraft(ctx, p, task, org, assetInput, "draft-valid-create", "")
	if err != nil {
		t.Fatal(err)
	}
	systemInput := BusinessSystemDraftInput{CreateInput: businesssystem.CreateInput{Code: "DRAFT_" + stamp, Name: strings.Repeat("系", 255), Status: "running", Level: "normal", InternetExposure: "unknown", ContainsPersonalInformation: "unknown", ContainsSensitivePersonalInformation: "unknown", DataVolumeOver100K: "unknown", ContainsImportantData: "unknown", ContainsCoreData: "unknown"}, CustomValues: []customfield.Value{{FieldKey: key, Value: float64(0)}}}
	createdSystem, err := svc.CreateBusinessSystemDraft(ctx, p, task, org, systemInput, "draft-valid-create", "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var result string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_object(
   'assets',(SELECT jsonb_agg(a ORDER BY id) FROM inventory_asset_drafts a WHERE created_by=$1::uuid),
   'systems',(SELECT jsonb_agg(s ORDER BY id) FROM inventory_business_system_drafts s WHERE created_by=$1::uuid),
   'tasks',(SELECT jsonb_agg(t ORDER BY id) FROM inventory_tasks t WHERE created_by=$1::uuid),
   'progress',(SELECT jsonb_agg(p ORDER BY task_id,organization_id) FROM inventory_task_organizations p WHERE task_id IN (SELECT id FROM inventory_tasks WHERE created_by=$1::uuid)),
   'events',(SELECT jsonb_agg(e ORDER BY id) FROM notification_events e WHERE actor_user_id=$1::uuid),
   'audits',(SELECT jsonb_agg(a ORDER BY id) FROM audit_logs a WHERE user_id=$1::uuid),
   'formal_assets',(SELECT count(*) FROM assets WHERE created_by=$1::uuid),
   'formal_systems',(SELECT count(*) FROM business_systems WHERE created_by=$1::uuid))::text`, user).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if createdSystem.Name != systemInput.Name || !reflect.DeepEqual(createdSystem.CustomValues, systemInput.CustomValues) || !reflect.DeepEqual(createdAsset.CustomValues, assetInput.CustomValues) {
		t.Fatal("legal draft creation changed full-length name or zero-valued custom field")
	}
	before := snapshot()
	for _, version := range []int{-1, 0} {
		assetInvalid := assetInput
		assetInvalid.ExpectedVersion = &version
		systemInvalid := systemInput
		systemInvalid.ExpectedVersion = &version
		_, createError := svc.CreateAssetDraft(ctx, p, task, org, assetInvalid, "draft-invalid-version", "")
		_, assetUpdateError := svc.UpdateAssetDraft(ctx, p, task, org, createdAsset.ID, assetInvalid, "draft-invalid-version", "")
		_, systemUpdateError := svc.UpdateBusinessSystemDraft(ctx, p, task, org, createdSystem.ID, systemInvalid, "draft-invalid-version", "")
		for _, err := range []error{createError, assetUpdateError, systemUpdateError,
			svc.DeleteAssetDraft(ctx, p, task, org, createdAsset.ID, version, "draft-invalid-version", ""),
			svc.DeleteBusinessSystemDraft(ctx, p, task, org, createdSystem.ID, version, "draft-invalid-version", "")} {
			var typed *InputValidationError
			if !errors.As(err, &typed) || typed.Message != "草稿版本无效，请刷新后重试" {
				t.Fatalf("invalid version: %v", err)
			}
		}
		if snapshot() != before {
			t.Fatal("invalid version mutated draft state, notification or audit")
		}
	}
	for _, tc := range []struct {
		name, message string
		change        func(*asset.CreateInput)
	}{
		{"value", "资产值与所选资产类型不匹配，请检查资产值", func(in *asset.CreateInput) { in.Value = "not-a-domain" }},
		{"environment", "请选择有效的部署环境", func(in *asset.CreateInput) { in.Environment = "invalid" }},
		{"task scope", "资产网络范围不符合本次填报任务的核查范围", func(in *asset.CreateInput) { in.NetworkScope = "private" }},
		{"network area", "网络区域标识：请选择有效的网络区域", func(in *asset.CreateInput) { in.NetworkAreaID = "unavailable-reference" }},
		{"business system", "业务系统标识：请选择有效的业务系统或工作版本", func(in *asset.CreateInput) { in.BusinessSystemID = "unavailable-reference" }},
		{"owner", "责任人账号标识：请选择有效的账号", func(in *asset.CreateInput) { in.OwnerUserID = "unavailable-reference" }},
		{"custom type", "自定义字段“草稿数字字段”：须为数字", func(in *asset.CreateInput) {
			in.CustomValues = []customfield.Value{{FieldKey: key, Value: "PRIVATE_VALUE"}}
		}},
		{"unknown custom key", "自定义字段编码无效，请刷新后重试", func(in *asset.CreateInput) {
			in.CustomValues = []customfield.Value{{FieldKey: "PRIVATE_KEY", Value: "PRIVATE_VALUE"}}
		}},
	} {
		for _, method := range []string{"create", "update"} {
			t.Run("asset/"+tc.name+"/"+method, func(t *testing.T) {
				in := assetInput
				v := createdAsset.Version
				in.ExpectedVersion = &v
				tc.change(&in)
				var err error
				if method == "create" {
					_, err = svc.CreateAssetDraft(ctx, p, task, org, in, "draft-rejected", "")
				} else {
					_, err = svc.UpdateAssetDraft(ctx, p, task, org, createdAsset.ID, in, "draft-rejected", "")
				}
				var typed *InputValidationError
				if !errors.Is(err, ErrInvalid) || !errors.As(err, &typed) || typed.Message != tc.message {
					t.Errorf("error=%v, want %q", err, tc.message)
				}
				if snapshot() != before {
					t.Fatal("rejected asset write changed drafts, task, progress, notification, audit or formal ledger")
				}
			})
		}
	}
	for _, tc := range []struct {
		name, message string
		change        func(*BusinessSystemDraftInput)
	}{
		{"name", "系统名称不能为空", func(in *BusinessSystemDraftInput) { in.Name = "　 " }},
		{"long name", "系统名称最多 255 字", func(in *BusinessSystemDraftInput) { in.Name = strings.Repeat("😀", 256) }},
		{"long short name", "系统简称最多 128 字", func(in *BusinessSystemDraftInput) { in.ShortName = strings.Repeat("🙂", 129) }},
		{"long owner", "负责人最多 128 字", func(in *BusinessSystemDraftInput) { in.OwnerName = strings.Repeat("中", 129) }},
		{"long deployment", "部署方式最多 32 字", func(in *BusinessSystemDraftInput) { in.DeploymentType = strings.Repeat("a", 33) }},
		{"contradiction", "包含敏感个人信息时，是否包含个人信息不能选择否", func(in *BusinessSystemDraftInput) {
			in.ContainsPersonalInformation = "no"
			in.ContainsSensitivePersonalInformation = "yes"
		}},
		{"custom type", "自定义字段“草稿数字字段”：须为数字", func(in *BusinessSystemDraftInput) {
			in.CustomValues = []customfield.Value{{FieldKey: key, Value: "PRIVATE_VALUE"}}
		}},
		{"duplicate custom key", "自定义字段“草稿数字字段”不能重复填写", func(in *BusinessSystemDraftInput) {
			in.CustomValues = []customfield.Value{{FieldKey: key, Value: float64(1)}, {FieldKey: key, Value: float64(2)}}
		}},
	} {
		for _, method := range []string{"create", "update"} {
			t.Run("system/"+tc.name+"/"+method, func(t *testing.T) {
				in := systemInput
				v := createdSystem.Version
				in.ExpectedVersion = &v
				tc.change(&in)
				var err error
				if method == "create" {
					_, err = svc.CreateBusinessSystemDraft(ctx, p, task, org, in, "draft-rejected", "")
				} else {
					_, err = svc.UpdateBusinessSystemDraft(ctx, p, task, org, createdSystem.ID, in, "draft-rejected", "")
				}
				var typed *InputValidationError
				if !errors.Is(err, ErrInvalid) || !errors.As(err, &typed) || typed.Message != tc.message {
					t.Errorf("error=%v, want %q", err, tc.message)
				}
				if snapshot() != before {
					t.Fatal("rejected system write changed drafts, task, progress, notification, audit or formal ledger")
				}
			})
		}
	}
	// Legal input still updates the actual draft using its original CAS token.
	assetVersion, systemVersion := createdAsset.Version, createdSystem.Version
	assetInput.ExpectedVersion = &assetVersion
	assetInput.Name = "合法资产更新"
	updatedAsset, err := svc.UpdateAssetDraft(ctx, p, task, org, createdAsset.ID, assetInput, "draft-valid-update", "")
	if err != nil || updatedAsset.Version != assetVersion+1 || updatedAsset.Name != assetInput.Name {
		t.Fatalf("valid asset update: version=%d err=%v", updatedAsset.Version, err)
	}
	systemInput.ExpectedVersion = &systemVersion
	systemInput.Name = strings.Repeat("😀", 255)
	systemInput.ShortName = strings.Repeat("🙂", 128)
	systemInput.OwnerName = strings.Repeat("中", 128)
	systemInput.DeploymentType = strings.Repeat("🙂", 32)
	updatedSystem, err := svc.UpdateBusinessSystemDraft(ctx, p, task, org, createdSystem.ID, systemInput, "draft-valid-update", "")
	if err != nil || updatedSystem.Version != systemVersion+1 || updatedSystem.Name != systemInput.Name || updatedSystem.ShortName != systemInput.ShortName || updatedSystem.OwnerName != systemInput.OwnerName || updatedSystem.DeploymentType != systemInput.DeploymentType {
		t.Fatalf("valid system update: version=%d err=%v", updatedSystem.Version, err)
	}
	before = snapshot()
	if _, err := svc.UpdateAssetDraft(ctx, p, task, org, createdAsset.ID, assetInput, "draft-stale", ""); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("stale asset token: %v", err)
	}
	if _, err := svc.UpdateBusinessSystemDraft(ctx, p, task, org, createdSystem.ID, systemInput, "draft-stale", ""); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("stale system token: %v", err)
	}
	if snapshot() != before {
		t.Fatal("stale CAS changed drafts or audit")
	}
	var auditCount, formalAssets, formalSystems int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM audit_logs WHERE user_id=$1::uuid), (SELECT count(*) FROM assets WHERE created_by=$1::uuid), (SELECT count(*) FROM business_systems WHERE created_by=$1::uuid)`, user).Scan(&auditCount, &formalAssets, &formalSystems); err != nil {
		t.Fatal(err)
	}
	if auditCount != 4 || formalAssets != 0 || formalSystems != 0 {
		t.Fatalf("expected only four draft audits and no formal publication: audits=%d assets=%d systems=%d", auditCount, formalAssets, formalSystems)
	}
	// Pre-fix drafts can contain 33–64 deployment characters. Publishing them
	// must return a safe field error instead of PostgreSQL's varchar(32) failure.
	exec(`UPDATE inventory_business_system_drafts SET deployment_type=$2,draft_status='submitted' WHERE id=$1::uuid`, createdSystem.ID, strings.Repeat("🙂", 33))
	before = snapshot()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = publishBusinessSystemDrafts(ctx, tx, p, task, org, "legacy-deployment-rejected", "")
	_ = tx.Rollback(ctx)
	var typed *InputValidationError
	if !errors.As(err, &typed) || typed.Message != "部署方式最多 32 字" {
		t.Errorf("legacy publication error=%v", err)
	}
	if snapshot() != before {
		t.Fatal("rejected legacy publication changed data")
	}
	exec(`UPDATE inventory_business_system_drafts SET deployment_type=$2 WHERE id=$1::uuid`, createdSystem.ID, systemInput.DeploymentType)
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := publishBusinessSystemDrafts(ctx, tx, p, task, org, "full-limit-publication", ""); err != nil {
		t.Fatal(err)
	}
	var name, short, owner, deployment string
	if err := tx.QueryRow(ctx, `SELECT system_name,short_name,owner_name,deployment_type FROM business_systems WHERE id=$1::uuid`, createdSystem.ID).Scan(&name, &short, &owner, &deployment); err != nil {
		t.Fatal(err)
	}
	if name != systemInput.Name || short != systemInput.ShortName || owner != systemInput.OwnerName || deployment != systemInput.DeploymentType {
		t.Fatal("published Unicode text was truncated")
	}

}
