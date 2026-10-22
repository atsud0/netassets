package inventorytask

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"netassets/internal/auth"
	"netassets/internal/customfield"
	"netassets/internal/exportfile"
	"netassets/internal/pagination"
)

func TestNormalizeCreateInput(t *testing.T) {
	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	input := CreateInput{
		Name:            "  季度填报  ",
		StartAt:         start,
		DeadlineAt:      start.Add(7 * 24 * time.Hour),
		Status:          "active",
		OrganizationIDs: []string{"org-1", "org-1", " org-2 "},
		RequiredFields:  []string{"network_assets", "network_assets", "business_systems"},
	}
	if err := normalizeCreateInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.Name != "季度填报" || len(input.OrganizationIDs) != 2 || len(input.RequiredFields) != 2 || input.AssetNetworkScope != "all" {
		t.Fatalf("unexpected normalized input: %+v", input)
	}
}

func TestPreviewScopeRejectsNonGroupAdmin(t *testing.T) {
	service := &Service{}
	_, err := service.PreviewScope(context.Background(), auth.Principal{Roles: []string{"organization_admin"}}, ScopePreviewInput{
		OrganizationIDs: []string{"org-1"}, RequiredFields: []string{"network_assets"},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("PreviewScope() error = %v, want ErrForbidden", err)
	}
}

func TestNormalizeScopePreviewInput(t *testing.T) {
	input := ScopePreviewInput{
		OrganizationIDs: []string{" org-1 ", "org-1", "org-2"},
		RequiredFields:  []string{"network_assets", "network_assets", "data_security"},
	}
	if err := normalizeScopePreviewInput(&input); err != nil {
		t.Fatal(err)
	}
	if len(input.OrganizationIDs) != 2 || len(input.RequiredFields) != 2 || input.AssetNetworkScope != "all" {
		t.Fatalf("unexpected normalized preview input: %+v", input)
	}
}

func TestNormalizeScopePreviewInputRejectsInvalidScope(t *testing.T) {
	input := ScopePreviewInput{
		OrganizationIDs: []string{"org-1"}, RequiredFields: []string{"network_assets"}, AssetNetworkScope: "unknown",
	}
	if err := normalizeScopePreviewInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeScopePreviewInput() error = %v, want ErrInvalid", err)
	}
}

func TestNormalizeScopePreviewInputRejectsNoResourceType(t *testing.T) {
	input := ScopePreviewInput{OrganizationIDs: []string{"org-1"}, RequiredFields: []string{"owner_contacts"}}
	if err := normalizeScopePreviewInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeScopePreviewInput() error = %v, want ErrInvalid", err)
	}
}

func TestNormalizeCreateInputAcceptsExplicitAssetNetworkScope(t *testing.T) {
	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	for _, scope := range []string{"public", "private"} {
		t.Run(scope, func(t *testing.T) {
			input := CreateInput{
				Name: "专项填报", StartAt: start, DeadlineAt: start.Add(24 * time.Hour),
				AssetNetworkScope: scope, OrganizationIDs: []string{"org-1"}, RequiredFields: []string{"network_assets"},
			}
			if err := normalizeCreateInput(&input); err != nil {
				t.Fatal(err)
			}
			if input.AssetNetworkScope != scope {
				t.Fatalf("asset scope = %q, want %q", input.AssetNetworkScope, scope)
			}
		})
	}
}

func TestNormalizeCreateInputRejectsInvalidAssetNetworkScope(t *testing.T) {
	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	input := CreateInput{
		Name: "无效范围", StartAt: start, DeadlineAt: start.Add(24 * time.Hour), AssetNetworkScope: "unknown",
		OrganizationIDs: []string{"org-1"}, RequiredFields: []string{"network_assets"},
	}
	if err := normalizeCreateInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeCreateInput() error = %v, want ErrInvalid", err)
	}
}

func TestNormalizeCreateInputRejectsNoResourceType(t *testing.T) {
	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	input := CreateInput{
		Name: "无资源任务", StartAt: start, DeadlineAt: start.Add(24 * time.Hour),
		OrganizationIDs: []string{"org-1"}, RequiredFields: []string{"owner_contacts"},
	}
	if err := normalizeCreateInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeCreateInput() error = %v, want ErrInvalid", err)
	}
}

func TestSelectedResourceTypes(t *testing.T) {
	tests := []struct {
		name        string
		fields      []string
		wantSystems bool
		wantAssets  bool
	}{
		{name: "business systems", fields: []string{"business_systems"}, wantSystems: true},
		{name: "data security implies systems", fields: []string{"data_security"}, wantSystems: true},
		{name: "network assets", fields: []string{"network_assets"}, wantAssets: true},
		{name: "owner contacts alone", fields: []string{"owner_contacts"}},
		{name: "both", fields: []string{"business_systems", "network_assets"}, wantSystems: true, wantAssets: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSystems, gotAssets := selectedResourceTypes(tt.fields)
			if gotSystems != tt.wantSystems || gotAssets != tt.wantAssets {
				t.Fatalf("selectedResourceTypes(%v) = (%v, %v), want (%v, %v)", tt.fields, gotSystems, gotAssets, tt.wantSystems, tt.wantAssets)
			}
		})
	}
}

func TestNormalizeCreateInputRejectsInvalidRange(t *testing.T) {
	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	input := CreateInput{Name: "无效任务", StartAt: start, DeadlineAt: start, OrganizationIDs: []string{"org-1"}}
	if err := normalizeCreateInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeCreateInput() error = %v, want ErrInvalid", err)
	}
}

func TestNormalizeReviewInput(t *testing.T) {
	input := ReviewInput{Decision: " returned ", ReturnReason: " 负责人信息不完整 "}
	if err := normalizeReviewInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.Decision != "returned" || input.ReturnReason != "负责人信息不完整" {
		t.Fatalf("unexpected normalized review input: %+v", input)
	}
}

func TestNormalizeReviewInputRequiresReturnReason(t *testing.T) {
	input := ReviewInput{Decision: "returned"}
	if err := normalizeReviewInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeReviewInput() error = %v, want ErrInvalid", err)
	}
}

func TestNormalizeReviewInputClearsApprovalReason(t *testing.T) {
	input := ReviewInput{Decision: "approved", ReturnReason: "unused"}
	if err := normalizeReviewInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.ReturnReason != "" {
		t.Fatalf("return reason = %q, want empty", input.ReturnReason)
	}
}

func TestBuildQualityCheckBlocksRequiredDataErrors(t *testing.T) {
	result := buildQualityCheck([]string{"business_systems", "network_assets", "owner_contacts", "data_security"}, qualityCounts{
		AssetCount:          2,
		MissingAssetOwner:   1,
		UnknownDataSecurity: 1,
	})
	if result.Passed {
		t.Fatal("quality check passed, want blocked")
	}
	if result.ErrorCount != 3 {
		t.Fatalf("error count = %d, want 3", result.ErrorCount)
	}
	if result.WarningCount != 0 {
		t.Fatalf("warning count = %d, want 0", result.WarningCount)
	}
}

func TestBuildQualityCheckAllowsWarnings(t *testing.T) {
	result := buildQualityCheck([]string{"business_systems", "network_assets"}, qualityCounts{
		SystemCount:          1,
		AssetCount:           2,
		UnlinkedPublicAssets: 1,
	})
	if !result.Passed {
		t.Fatal("quality check blocked on warning")
	}
	if result.WarningCount != 1 || result.ErrorCount != 0 {
		t.Fatalf("unexpected counts: errors=%d warnings=%d", result.ErrorCount, result.WarningCount)
	}
}

func TestBuildQualityCheckSkipsUnselectedResourceTypeOwners(t *testing.T) {
	result := buildQualityCheck([]string{"business_systems", "owner_contacts"}, qualityCounts{
		SystemCount:       1,
		MissingAssetOwner: 8,
	})
	if !result.Passed || result.ErrorCount != 0 || result.WarningCount != 0 {
		t.Fatalf("unselected asset issues must be ignored: %+v", result)
	}

	result = buildQualityCheck([]string{"network_assets", "owner_contacts"}, qualityCounts{
		AssetCount:         1,
		MissingSystemOwner: 5,
	})
	if !result.Passed || result.ErrorCount != 0 || result.WarningCount != 0 {
		t.Fatalf("unselected system issues must be ignored: %+v", result)
	}
}

func TestBuildInventoryTaskExportCSV(t *testing.T) {
	startAt := time.Date(2026, time.July, 22, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	submittedAt := startAt.Add(2 * time.Hour)
	reviewedAt := submittedAt.Add(time.Hour)
	task := Task{
		Name: "第三季度/网络资产填报", Description: "集团统一口径", StartAt: startAt, DeadlineAt: startAt.Add(7 * 24 * time.Hour),
		Status: "completed", AssetNetworkScope: "public", RequireReview: true, AllowLateSubmission: false, TargetCount: 1, SubmittedCount: 1, ApprovedCount: 1,
		CreatedByName: "集团管理员", CreatedAt: startAt.Add(-time.Hour),
		Organizations: []OrganizationProgress{{
			OrganizationCode: "SUB_A", OrganizationName: "甲公司", Status: "approved", StartedAt: &startAt,
			SubmittedAt: &submittedAt, ReviewedAt: &reviewedAt, SubmissionNote: "已完成填报", SubmittedSystemCount: 3,
			SubmittedAssetCount: 12, SubmittedByName: "子公司管理员", ReviewedByName: "集团审核员",
		}},
	}
	data, contentType, fileName, err := buildExport(task, "csv")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/csv; charset=utf-8" || fileName != "填报任务结果-第三季度-网络资产填报.csv" {
		t.Fatalf("unexpected metadata: %q %q", contentType, fileName)
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][1] != "已完成" || rows[1][3] != "仅公网资产" || rows[1][6] != "是" || rows[1][7] != "否" || rows[1][13] != "已通过" {
		t.Fatalf("unexpected status fields: %#v", rows)
	}
	if rows[1][17] != "3" || rows[1][18] != "12" || rows[1][19] != "已完成填报" || rows[1][21] != "集团审核员" {
		t.Fatalf("unexpected result fields: %#v", rows[1])
	}
	if rows[1][15] != "2026-07-22 11:00:00+08:00" || rows[1][20] != "2026-07-22 12:00:00+08:00" {
		t.Fatalf("unexpected timestamps: %#v", rows[1])
	}
}

func TestBuildInventoryTaskExportXLSXAndRejectInvalidFormat(t *testing.T) {
	task := Task{Name: "测试任务", Status: "active", AssetNetworkScope: "all", Organizations: []OrganizationProgress{{OrganizationCode: "SUB_A", OrganizationName: "甲公司", Status: "not_started"}}}
	data, contentType, fileName, err := buildExport(task, "xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contentType, "spreadsheetml") || fileName != "填报任务结果-测试任务.xlsx" {
		t.Fatalf("unexpected metadata: %q %q", contentType, fileName)
	}
	file, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := file.GetRows("台账")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "任务名称" || rows[1][3] != "全部网络资产" || rows[1][11] != "SUB_A" || rows[1][13] != "未开始" {
		t.Fatalf("unexpected XLSX rows: %#v", rows)
	}
	if _, _, _, err := buildExport(task, "xls"); err != ErrInvalid {
		t.Fatalf("buildExport(xls) error = %v", err)
	}
}

func TestBuildSubmissionDiffTracksAddedModifiedAndRemovedResources(t *testing.T) {
	previous := []snapshotItem{
		{
			ResourceType: "business_system", ResourceID: "system-1", ResourceKey: "SYS-001", ResourceName: "旧系统名称",
			Data: map[string]any{"system_code": "SYS-001", "system_name": "旧系统名称", "system_level": "normal"},
		},
		{
			ResourceType: "network_asset", ResourceID: "asset-removed", ResourceKey: "domain:old.example.com", ResourceName: "old.example.com",
			Data: map[string]any{"asset_type": "domain", "asset_value": "old.example.com"},
		},
	}
	current := []snapshotItem{
		{
			ResourceType: "business_system", ResourceID: "system-1", ResourceKey: "SYS-001", ResourceName: "新系统名称",
			Data: map[string]any{"system_code": "SYS-001", "system_name": "新系统名称", "system_level": "important"},
		},
		{
			ResourceType: "network_asset", ResourceID: "asset-added", ResourceKey: "url:https://new.example.com", ResourceName: "新入口",
			Data: map[string]any{"asset_type": "url", "asset_value": "https://new.example.com"},
		},
	}

	result := buildSubmissionDiff(current, previous)
	if result.Summary.Added != 1 || result.Summary.Modified != 1 || result.Summary.Removed != 1 {
		t.Fatalf("unexpected summary: %+v", result.Summary)
	}
	if len(result.BusinessSystems.Modified) != 1 || len(result.BusinessSystems.Modified[0].Changes) != 2 {
		t.Fatalf("unexpected system diff: %+v", result.BusinessSystems)
	}
	if result.BusinessSystems.Modified[0].Changes[0].Field != "system_name" || result.BusinessSystems.Modified[0].Changes[1].Field != "system_level" {
		t.Fatalf("unexpected field order: %+v", result.BusinessSystems.Modified[0].Changes)
	}
	if len(result.NetworkAssets.Added) != 1 || len(result.NetworkAssets.Removed) != 1 {
		t.Fatalf("unexpected asset diff: %+v", result.NetworkAssets)
	}
}

func TestBuildSubmissionDiffUsesInventoryDraftOperationsWithoutDoubleCounting(t *testing.T) {
	previous := []snapshotItem{
		{
			ResourceType: "business_system", ResourceID: "system-source", ResourceKey: "SYS-OLD", ResourceName: "旧系统",
			Data: map[string]any{"system_code": "SYS-OLD", "system_name": "旧系统", "system_level": "normal"},
		},
		{
			ResourceType: "network_asset", ResourceID: "asset-source", ResourceKey: "domain:old.example.com", ResourceName: "旧域名",
			Data: map[string]any{"asset_type": "domain", "asset_value": "old.example.com", "asset_name": "旧域名"},
		},
	}
	current := []snapshotItem{
		{
			ResourceType: "business_system", ResourceID: "system-draft", ResourceKey: "SYS-NEW", ResourceName: "新系统",
			Data: map[string]any{
				"system_code": "SYS-NEW", "system_name": "新系统", "system_level": "important",
				inventoryOperationKey: "update", inventorySourceIDKey: "system-source",
				inventoryBeforeKey: map[string]any{"system_code": "SYS-OLD", "system_name": "旧系统", "system_level": "normal"},
			},
		},
		{
			ResourceType: "network_asset", ResourceID: "asset-delete-draft", ResourceKey: "domain:old.example.com", ResourceName: "旧域名",
			Data: map[string]any{
				"asset_type": "domain", "asset_value": "old.example.com", "asset_name": "旧域名",
				inventoryOperationKey: "delete", inventorySourceIDKey: "asset-source",
				inventoryBeforeKey: map[string]any{"asset_type": "domain", "asset_value": "old.example.com", "asset_name": "旧域名"},
			},
		},
		{
			ResourceType: "network_asset", ResourceID: "asset-create-draft", ResourceKey: "domain:new.example.com", ResourceName: "新域名",
			Data: map[string]any{
				"asset_type": "domain", "asset_value": "new.example.com", "asset_name": "新域名",
				inventoryOperationKey: "create",
			},
		},
	}

	result := buildSubmissionDiff(current, previous)
	if result.Summary.Added != 1 || result.Summary.Modified != 1 || result.Summary.Removed != 1 {
		t.Fatalf("draft operations must be counted once: %+v", result.Summary)
	}
	if result.BusinessSystems.Modified[0].Operation != "update" || len(result.BusinessSystems.Modified[0].Changes) != 3 {
		t.Fatalf("unexpected update draft diff: %+v", result.BusinessSystems.Modified)
	}
	if result.NetworkAssets.Added[0].Operation != "create" || result.NetworkAssets.Removed[0].Operation != "delete" {
		t.Fatalf("unexpected create/delete draft operations: %+v", result.NetworkAssets)
	}
}

func TestBuildSubmissionDiffTreatsFirstSubmissionAsAddedBaseline(t *testing.T) {
	current := []snapshotItem{
		{ResourceType: "business_system", ResourceID: "system-1", ResourceKey: "SYS-001", ResourceName: "系统一", Data: map[string]any{"system_code": "SYS-001"}},
		{ResourceType: "network_asset", ResourceID: "asset-1", ResourceKey: "ip:192.0.2.1", ResourceName: "192.0.2.1", Data: map[string]any{"asset_type": "ip"}},
	}
	result := buildSubmissionDiff(current, nil)
	if result.Summary.Added != 2 || result.Summary.Modified != 0 || result.Summary.Removed != 0 {
		t.Fatalf("unexpected first submission summary: %+v", result.Summary)
	}
	if result.BusinessSystems.Added == nil || result.NetworkAssets.Modified == nil {
		t.Fatal("diff arrays must be serialized as empty arrays instead of null")
	}
}

func TestNormalizeReviewInputWithResourceComments(t *testing.T) {
	input := ReviewInput{
		Decision:     " returned ",
		ReturnReason: " 请逐条整改 ",
		Comments: []ReviewCommentInput{
			{ResourceType: " business_system ", ResourceID: " system-1 ", Comment: " 补充负责人联系方式 "},
			{ResourceType: "network_asset", ResourceID: "asset-1", Comment: "确认该公网域名是否仍在使用"},
		},
	}
	if err := normalizeReviewInput(&input); err != nil {
		t.Fatal(err)
	}
	if len(input.Comments) != 2 || input.Comments[0].ResourceType != "business_system" ||
		input.Comments[0].ResourceID != "system-1" || input.Comments[0].Comment != "补充负责人联系方式" {
		t.Fatalf("unexpected normalized comments: %+v", input.Comments)
	}
}

func TestNormalizeReviewInputRejectsDuplicateResourceComments(t *testing.T) {
	input := ReviewInput{
		Decision:     "returned",
		ReturnReason: "请整改",
		Comments: []ReviewCommentInput{
			{ResourceType: "network_asset", ResourceID: "asset-1", Comment: "意见一"},
			{ResourceType: "network_asset", ResourceID: "asset-1", Comment: "意见二"},
		},
	}
	if err := normalizeReviewInput(&input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeReviewInput() error = %v, want ErrInvalid", err)
	}
}

func TestNormalizeReviewInputClearsCommentsOnApproval(t *testing.T) {
	input := ReviewInput{
		Decision: "approved",
		Comments: []ReviewCommentInput{{ResourceType: "network_asset", ResourceID: "asset-1", Comment: "unused"}},
	}
	if err := normalizeReviewInput(&input); err != nil {
		t.Fatal(err)
	}
	if len(input.Comments) != 0 {
		t.Fatalf("approval comments = %+v, want empty", input.Comments)
	}
}

func TestNormalizeReviewCommentsRejectsTooMany(t *testing.T) {
	comments := make([]ReviewCommentInput, 101)
	for index := range comments {
		comments[index] = ReviewCommentInput{ResourceType: "network_asset", ResourceID: "asset-many", Comment: "整改"}
	}
	if _, err := normalizeReviewComments(comments); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeReviewComments() error = %v, want ErrInvalid", err)
	}
}

func TestBuildSubmissionDiffTracksCustomFieldValueWithHistoricalLabel(t *testing.T) {
	previous := []snapshotItem{{
		ResourceType: "business_system", ResourceID: "system-1", ResourceKey: "SYS-001", ResourceName: "系统一",
		Data: map[string]any{
			"system_code":                  "SYS-001",
			"custom:data_retention_period": "180 天",
			"_custom_field_labels": map[string]any{
				"custom:data_retention_period": "数据保留期限",
			},
		},
	}}
	current := []snapshotItem{{
		ResourceType: "business_system", ResourceID: "system-1", ResourceKey: "SYS-001", ResourceName: "系统一",
		Data: map[string]any{
			"system_code":                  "SYS-001",
			"custom:data_retention_period": "365 天",
			"_custom_field_labels": map[string]any{
				"custom:data_retention_period": "保留期限（新名称）",
			},
		},
	}}

	result := buildSubmissionDiff(current, previous)
	changes := result.BusinessSystems.Modified[0].Changes
	if len(changes) != 1 {
		t.Fatalf("unexpected changes: %+v", changes)
	}
	if changes[0].Field != "custom:data_retention_period" || changes[0].Label != "自定义字段：保留期限（新名称）" || changes[0].Before != "180 天" || changes[0].After != "365 天" {
		t.Fatalf("unexpected custom field change: %+v", changes[0])
	}
}

func TestBuildSubmissionDiffIgnoresCustomMultiSelectOrder(t *testing.T) {
	previous := []snapshotItem{{
		ResourceType: "network_asset", ResourceID: "asset-1", ResourceKey: "domain:example.com", ResourceName: "example.com",
		Data: map[string]any{
			"custom:security_tags": []any{"互联网", "重要"},
			"_custom_field_labels": map[string]any{"custom:security_tags": "安全标签"},
		},
	}}
	current := []snapshotItem{{
		ResourceType: "network_asset", ResourceID: "asset-1", ResourceKey: "domain:example.com", ResourceName: "example.com",
		Data: map[string]any{
			"custom:security_tags": []any{"重要", "互联网"},
			"_custom_field_labels": map[string]any{"custom:security_tags": "安全标签"},
		},
	}}

	result := buildSubmissionDiff(current, previous)
	if len(result.NetworkAssets.Modified) != 0 || result.Summary.Modified != 0 {
		t.Fatalf("same multiselect values in different order must not produce a diff: %+v", result)
	}
}

func TestBuildSubmissionDiffUsesHistoricalCustomFieldLabelWhenCurrentSnapshotDoesNotHaveOne(t *testing.T) {
	changes := compareSnapshotData("network_asset",
		map[string]any{
			"custom:business_owner": "甲", "_custom_field_labels": map[string]any{"custom:business_owner": "业务归口人"},
		},
		map[string]any{"custom:business_owner": "乙"},
	)
	if len(changes) != 1 || changes[0].Label != "自定义字段：业务归口人" {
		t.Fatalf("expected historical custom label, got %+v", changes)
	}
}

func TestCompareSnapshotDataIgnoresInventoryDraftMarker(t *testing.T) {
	changes := compareSnapshotData("network_asset",
		map[string]any{
			"asset_value":      "draft.example.com",
			"_inventory_draft": false,
		},
		map[string]any{
			"asset_value":      "draft.example.com",
			"_inventory_draft": true,
		},
	)
	if len(changes) != 0 {
		t.Fatalf("inventory draft marker must not produce a business field diff: %+v", changes)
	}
}

func TestBuildInventoryTaskExportRejectsOversizedSynchronousXLSX(t *testing.T) {
	task := Task{Organizations: make([]OrganizationProgress, exportfile.MaxSynchronousXLSXRows+1)}
	if _, _, _, err := buildExport(task, "xlsx"); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("buildExport oversized xlsx error = %v, want ErrExportTooLarge", err)
	}
}

func TestWorkingVersionDiffRejectsInvalidResourceTypeBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	_, err := service.WorkingVersionDiff(context.Background(), auth.Principal{Roles: []string{"group_admin"}}, "certificate", "draft-1")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("WorkingVersionDiff() error = %v, want ErrInvalid", err)
	}
}

func TestWorkingVersionDiffRejectsMissingResourcePermissionBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	_, err := service.WorkingVersionDiff(context.Background(), auth.Principal{Roles: []string{"organization_user"}}, "network_asset", "draft-1")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("WorkingVersionDiff() error = %v, want ErrForbidden", err)
	}
}

func TestResourceSubmissionHistoryRejectsInvalidResourceBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	page := pagination.Query{Page: 1, PageSize: 20}
	principal := auth.Principal{Roles: []string{"group_admin"}}
	if _, err := service.ResourceSubmissionHistory(context.Background(), principal, "server", "not-a-uuid", page); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ResourceSubmissionHistory() error = %v, want ErrInvalid", err)
	}
}

func TestResourceSubmissionHistoryChecksPermissionBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	page := pagination.Query{Page: 1, PageSize: 20}
	principal := auth.Principal{Roles: []string{"organization_viewer"}}
	if _, err := service.ResourceSubmissionHistory(context.Background(), principal, "network_asset", "2cf486e9-71c8-4a48-82e4-17e3ab76b53d", page); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ResourceSubmissionHistory() error = %v, want ErrForbidden", err)
	}
}

func TestBuildResourceSubmissionChangesUsesFrozenBeforeSnapshot(t *testing.T) {
	snapshot := map[string]any{
		"asset_name":       "新名称",
		inventoryBeforeKey: map[string]any{"asset_name": "旧名称"},
	}
	operation, changes := buildResourceSubmissionChanges("network_asset", "update", snapshot, map[string]any{"asset_name": "更早名称"})
	if operation != "update" || len(changes) != 1 {
		t.Fatalf("operation=%q changes=%+v", operation, changes)
	}
	if changes[0].Before != "旧名称" || changes[0].After != "新名称" {
		t.Fatalf("change=%+v, want frozen submit-time values", changes[0])
	}
}

func TestBuildResourceSubmissionChangesIncludesCustomFields(t *testing.T) {
	snapshot := map[string]any{
		"system_name":          "客户系统",
		"custom:data_class":    "敏感",
		"_custom_field_labels": map[string]any{"custom:data_class": "数据分类"},
	}
	operation, changes := buildResourceSubmissionChanges("business_system", "create", snapshot, nil)
	if operation != "create" {
		t.Fatalf("operation=%q, want create", operation)
	}
	found := false
	for _, change := range changes {
		if change.Field == "custom:data_class" && change.Label == "自定义字段：数据分类" && change.After == "敏感" {
			found = true
		}
	}
	if !found {
		t.Fatalf("custom field change missing: %+v", changes)
	}
}

func TestBuildResourceSubmissionChangesClassifiesUnchangedSnapshots(t *testing.T) {
	current := map[string]any{"system_name": "统一门户"}
	operation, changes := buildResourceSubmissionChanges("business_system", "", current, map[string]any{"system_name": "统一门户"})
	if operation != "confirmed" || len(changes) != 0 {
		t.Fatalf("operation=%q changes=%+v, want confirmed without changes", operation, changes)
	}

	operation, changes = buildResourceSubmissionChanges("business_system", "", current, nil)
	if operation != "first_report" || len(changes) != 1 {
		t.Fatalf("operation=%q changes=%+v, want first_report", operation, changes)
	}
}

func TestRestoreResourceSubmissionRejectsInvalidIdentifiersBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	principal := auth.Principal{Roles: []string{"organization_admin"}, Permissions: []string{"inventory.manage", "asset.manage"}}
	_, err := service.RestoreResourceSubmission(context.Background(), principal, "network_asset", "invalid", "also-invalid", ResourceSubmissionRestoreInput{}, "", "")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("RestoreResourceSubmission() error = %v, want ErrInvalid", err)
	}
}

func TestRestoreResourceSubmissionRejectsGroupAdminBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	principal := auth.Principal{Roles: []string{"group_admin"}, Permissions: []string{"inventory.manage", "asset.manage"}}
	_, err := service.RestoreResourceSubmission(context.Background(), principal, "network_asset", "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", ResourceSubmissionRestoreInput{}, "", "")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("RestoreResourceSubmission() error = %v, want ErrForbidden", err)
	}
}

func TestRestoreResourceSubmissionRequiresInventoryAndResourcePermissionsBeforeDatabaseAccess(t *testing.T) {
	service := &Service{}
	resourceID := "11111111-1111-4111-8111-111111111111"
	submissionID := "22222222-2222-4222-8222-222222222222"
	cases := []auth.Principal{
		{Roles: []string{"organization_admin"}, Permissions: []string{"asset.manage"}},
		{Roles: []string{"organization_admin"}, Permissions: []string{"inventory.manage"}},
	}
	for _, principal := range cases {
		_, err := service.RestoreResourceSubmission(context.Background(), principal, "network_asset", resourceID, submissionID, ResourceSubmissionRestoreInput{}, "", "")
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("permissions=%v error=%v, want ErrForbidden", principal.Permissions, err)
		}
	}
}

func TestMergeRestoredCustomValuesUsesValidHistoryAndPreservesInvalidCurrentValue(t *testing.T) {
	definitions := []customfield.Definition{
		{FieldKey: "classification", FieldName: "数据分类", FieldType: "select", Options: []string{"一般", "敏感"}, IsRequired: true},
		{FieldKey: "retention", FieldName: "保留期限", FieldType: "number"},
	}
	current := []customfield.Value{
		{FieldKey: "classification", Value: "一般"},
		{FieldKey: "retention", Value: float64(30)},
	}
	snapshot := map[string]any{
		"custom:classification": "敏感",
		"custom:retention":      "已失效的数字格式",
		"custom:disabled":       "旧值",
		customFieldLabelsKey: map[string]any{
			"custom:disabled": "已停用字段",
		},
	}

	values, warnings, err := mergeRestoredCustomValues(definitions, current, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0].FieldKey != "classification" || values[0].Value != "敏感" || values[1].Value != float64(30) {
		t.Fatalf("unexpected merged values: %+v", values)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings=%+v, want invalid and disabled warnings", warnings)
	}
}

func TestMergeRestoredCustomValuesRejectsMissingCurrentRequiredField(t *testing.T) {
	definitions := []customfield.Definition{{FieldKey: "required", FieldName: "当前必填", FieldType: "text", IsRequired: true}}
	_, _, err := mergeRestoredCustomValues(definitions, nil, map[string]any{})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("mergeRestoredCustomValues() error = %v, want ErrConflict", err)
	}
}

func TestRestoreInt64AcceptsWholeJSONNumbers(t *testing.T) {
	if value, ok := restoreInt64(float64(100000)); !ok || value != 100000 {
		t.Fatalf("restoreInt64() = %d, %v", value, ok)
	}
	if _, ok := restoreInt64(1.5); ok {
		t.Fatal("fractional JSON number must be rejected")
	}
}

// TestNormalizeCreateInputAcceptsFullLengthChineseDescription is the guard for the
// reported defect "填报任务 创建填报任务，任务说明输入过程报错".
//
// The dialog advertises 任务说明 最多 5000 字, but the service measured it with
// len(), which counts UTF-8 BYTES. A Chinese glyph is three bytes, so a perfectly
// normal explanation of 2000 字 was rejected as "too long" at about a third of
// the advertised limit. Postgres and the browser both count characters, so the
// service must too.
func TestNormalizeCreateInputAcceptsFullLengthChineseDescription(t *testing.T) {
	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	input := CreateInput{
		Name:        "2026 年第三季度网络资产填报",
		Description: strings.Repeat("本季度网络资产填报范围说明。", 227), // 4540 characters
		StartAt:     start,
		DeadlineAt:  start.Add(24 * time.Hour),
		// Name is stored in varchar(255) which Postgres counts in characters.
		OrganizationIDs: []string{"11111111-1111-4111-8111-111111111111"},
		RequiredFields:  []string{"business_systems", "network_assets"},
	}
	if err := normalizeCreateInput(&input); err != nil {
		t.Fatalf("4540 个中文字的任务说明被拒绝: %v（应按字符而非字节计长）", err)
	}
	// The advertised limit is still enforced, in the same unit.
	over := input
	over.Description = strings.Repeat("测", 5001)
	if err := normalizeCreateInput(&over); !errors.Is(err, ErrInvalid) {
		t.Fatalf("5001 字的任务说明 error = %v, want ErrInvalid", err)
	}
	over.Description = strings.Repeat("测", 5000)
	if err := normalizeCreateInput(&over); err != nil {
		t.Fatalf("恰好 5000 字的任务说明被拒绝: %v", err)
	}
}

// TestNormalizeCreateInputAcceptsFullLengthChineseName covers the same defect on
// the 255-character task name: 85 中文字 used to be the de-facto maximum.
func TestNormalizeCreateInputAcceptsFullLengthChineseName(t *testing.T) {
	start := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	input := CreateInput{
		Name:            strings.Repeat("填", 255),
		StartAt:         start,
		DeadlineAt:      start.Add(24 * time.Hour),
		OrganizationIDs: []string{"11111111-1111-4111-8111-111111111111"},
		RequiredFields:  []string{"network_assets"},
	}
	if err := normalizeCreateInput(&input); err != nil {
		t.Fatalf("255 个中文字的任务名称被拒绝: %v（应按字符而非字节计长）", err)
	}
	over := input
	over.Name = strings.Repeat("填", 256)
	if err := normalizeCreateInput(&over); !errors.Is(err, ErrInvalid) {
		t.Fatalf("256 字的任务名称 error = %v, want ErrInvalid", err)
	}
}
