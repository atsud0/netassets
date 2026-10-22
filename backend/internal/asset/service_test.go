package asset

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"netassets/internal/auth"
	"netassets/internal/customfield"
	"netassets/internal/exportfile"
	"netassets/internal/importmapping"
)

func TestImportsUseWorkingInventoryOutsideGroupAdmin(t *testing.T) {
	if !usesWorkingInventory(auth.Principal{Roles: []string{"organization_admin"}}) {
		t.Fatal("organization administrator import must use the organization working inventory")
	}
	if !usesWorkingInventory(auth.Principal{Roles: []string{"organization_editor"}}) {
		t.Fatal("organization editor import must use the organization working inventory")
	}
	if usesWorkingInventory(auth.Principal{Roles: []string{"group_admin"}}) {
		t.Fatal("group administrator import must continue writing the formal ledger")
	}
}

func TestNormalizeImportMode(t *testing.T) {
	organizationUser := auth.Principal{Roles: []string{"organization_admin"}}
	groupAdmin := auth.Principal{Roles: []string{"group_admin"}}

	for _, input := range []string{"", "  " + ImportModeSkipDuplicates + "  "} {
		mode, err := normalizeImportMode(organizationUser, input)
		if err != nil || mode != ImportModeSkipDuplicates {
			t.Fatalf("normalizeImportMode(%q) = %q, %v; want %q", input, mode, err, ImportModeSkipDuplicates)
		}
	}
	mode, err := normalizeImportMode(organizationUser, ImportModeUpdateWorking)
	if err != nil || mode != ImportModeUpdateWorking {
		t.Fatalf("organization update mode = %q, %v; want %q", mode, err, ImportModeUpdateWorking)
	}
	if _, err := normalizeImportMode(groupAdmin, ImportModeUpdateWorking); !errors.Is(err, ErrInvalid) {
		t.Fatalf("group administrator update mode error = %v, want ErrInvalid", err)
	}
	if _, err := normalizeImportMode(organizationUser, "replace_formal"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid import mode error = %v, want ErrInvalid", err)
	}
}

func TestImportTargetMutable(t *testing.T) {
	tests := []struct {
		name   string
		target importUpdateTarget
		want   bool
	}{
		{name: "formal asset", target: importUpdateTarget{Kind: "formal"}, want: true},
		{name: "unbound create working version", target: importUpdateTarget{Kind: "draft", DraftStatus: "editing", Operation: "create"}, want: true},
		{name: "unbound update working version", target: importUpdateTarget{Kind: "draft", DraftStatus: "editing", Operation: "update"}, want: true},
		{name: "task working version", target: importUpdateTarget{Kind: "draft", DraftStatus: "editing", TaskID: "task-1", Operation: "update"}, want: false},
		{name: "submitted working version", target: importUpdateTarget{Kind: "draft", DraftStatus: "submitted", Operation: "update"}, want: false},
		{name: "pending deletion", target: importUpdateTarget{Kind: "draft", DraftStatus: "editing", Operation: "delete"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := importTargetMutable(test.target); got != test.want {
				t.Fatalf("importTargetMutable(%#v) = %t, want %t", test.target, got, test.want)
			}
		})
	}
}

func TestNormalizeValue(t *testing.T) {
	tests := []struct {
		name      string
		assetType string
		value     string
		want      string
		wantError bool
	}{
		{name: "domain lower case and trailing dot", assetType: "domain", value: "Portal.Example.COM.", want: "portal.example.com"},
		{name: "IPv4", assetType: "ip", value: "192.0.2.10", want: "192.0.2.10"},
		{name: "IPv6 canonical", assetType: "ip", value: "2001:0db8::1", want: "2001:db8::1"},
		{name: "URL normalizes host and removes fragment", assetType: "url", value: "https://Portal.Example.COM/a?q=1#part", want: "https://portal.example.com/a?q=1"},
		{name: "service", assetType: "service", value: "API.Example.com:443", want: "api.example.com:443"},
		{name: "bad domain", assetType: "domain", value: "localhost", wantError: true},
		{name: "bad IP", assetType: "ip", value: "999.1.1.1", wantError: true},
		{name: "bad URL scheme", assetType: "url", value: "ftp://example.com", wantError: true},
		{name: "bad service port", assetType: "service", value: "example.com:70000", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeValue(tt.assetType, tt.value)
			if tt.wantError {
				if err == nil {
					t.Fatalf("normalizeValue() error = nil, want error; got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeValue() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestImportHeaderAndChineseEnums(t *testing.T) {
	headers := mapHeaders([]string{"\ufeff资产类型", "资产值", "资产名称", "业务系统编码", "环境", "网络暴露范围", "网络区域", "重要程度", "责任人账号", "负责人", "备注"})
	input, err := importInput([]string{"域名", "Portal.Example.com", "门户", "SYS_001", "生产", "公网", "上海办事处", "核心", "zhangsan", "张三", "测试资产"}, headers, "org-id")
	if err != nil {
		t.Fatal(err)
	}
	if input.Type != "domain" || input.Environment != "production" || input.NetworkScope != "public" || input.Importance != "core" {
		t.Fatalf("unexpected translated input: %+v", input)
	}
	if input.BusinessSystemID != "SYS_001" || input.NetworkAreaID != "上海办事处" || input.OrganizationID != "org-id" || input.OwnerUserID != "zhangsan" {
		t.Fatalf("unexpected references: %+v", input)
	}
}

func TestTemplateCSV(t *testing.T) {
	data, contentType, fileName, err := Template("csv")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/csv; charset=utf-8" || !strings.HasSuffix(fileName, ".csv") {
		t.Fatalf("unexpected metadata: %q %q", contentType, fileName)
	}
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "资产类型" || rows[0][1] != "资产值" || rows[0][8] != "责任人账号" || rows[1][8] != "zhangsan" || rows[0][10] != "有效期至" || rows[1][10] != "2027-12-31" {
		t.Fatalf("unexpected CSV template: %#v", rows)
	}
}

func TestTemplateXLSX(t *testing.T) {
	data, contentType, fileName, err := Template("xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(contentType, "spreadsheetml") || !strings.HasSuffix(fileName, ".xlsx") {
		t.Fatalf("unexpected metadata: %q %q", contentType, fileName)
	}
	file, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	rows, err := file.GetRows(file.GetSheetList()[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "资产类型" || rows[0][1] != "资产值" || rows[0][8] != "责任人账号" || rows[1][8] != "zhangsan" || rows[0][10] != "有效期至" || rows[1][10] != "2027-12-31" {
		t.Fatalf("unexpected XLSX template: %#v", rows)
	}
}

func TestFilterExportAssets(t *testing.T) {
	items := []Asset{
		{OrganizationID: "org-a", OrganizationCode: "SUB_A", OrganizationName: "甲公司", Type: "domain", Status: "active", Value: "portal.example.com", Name: "门户", BusinessSystemCode: "PORTAL_001", OwnerName: "张三"},
		{OrganizationID: "org-b", OrganizationCode: "SUB_B", OrganizationName: "乙公司", Type: "ip", Status: "disabled", Value: "192.0.2.10", Name: "旧服务器", OwnerName: "李四"},
	}

	filtered := filterExportAssets(items, ExportFilter{OrganizationID: "org-a", Type: "domain", Status: "active", Keyword: "portal_001"})
	if len(filtered) != 1 || filtered[0].Value != "portal.example.com" {
		t.Fatalf("unexpected filtered assets: %#v", filtered)
	}
	if got := filterExportAssets(items, ExportFilter{Keyword: "乙公司"}); len(got) != 1 || got[0].OrganizationID != "org-b" {
		t.Fatalf("organization keyword did not match: %#v", got)
	}
}

func TestNormalizeSelectedExportIDs(t *testing.T) {
	first := "11111111-1111-4111-8111-111111111111"
	second := "22222222-2222-4222-8222-222222222222"

	got, err := normalizeSelectedExportIDs([]string{"  " + first + "  ", first, second})
	if err != nil {
		t.Fatalf("normalize selected export IDs: %v", err)
	}
	if len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("unexpected normalized IDs: %#v", got)
	}
	if got, err := normalizeSelectedExportIDs(nil); err != nil || got != nil {
		t.Fatalf("normalize nil IDs = %#v, %v; want nil, nil", got, err)
	}
	if _, err := normalizeSelectedExportIDs([]string{"not-a-uuid"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid UUID error = %v, want ErrInvalid", err)
	}

	tooMany := make([]string, 0, MaxSelectedExportRows+1)
	for index := 0; index <= MaxSelectedExportRows; index++ {
		tooMany = append(tooMany, fmt.Sprintf("00000000-0000-4000-8000-%012d", index))
	}
	if _, err := normalizeSelectedExportIDs(tooMany); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized selection error = %v, want ErrInvalid", err)
	}
}

func TestFilterExportAssetsBySelectedIDs(t *testing.T) {
	items := []Asset{
		{ID: "asset-a", OrganizationID: "org-a", Type: "domain", Status: "active"},
		{ID: "asset-b", OrganizationID: "org-a", Type: "ip", Status: "active"},
		{ID: "asset-c", OrganizationID: "org-b", Type: "domain", Status: "disabled"},
	}
	got := filterExportAssets(items, ExportFilter{SelectedIDs: []string{"asset-a", "asset-c"}, OrganizationID: "org-a"})
	if len(got) != 1 || got[0].ID != "asset-a" {
		t.Fatalf("unexpected selected export filter result: %#v", got)
	}
}

func TestBuildAssetExportCSV(t *testing.T) {
	createdAt := time.Date(2026, time.July, 22, 10, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	expiresAt := time.Date(2027, time.December, 31, 23, 59, 59, 0, time.FixedZone("CST", 8*60*60))
	data, contentType, fileName, err := buildExport([]Asset{{
		OrganizationCode: "SUB_A", OrganizationName: "甲公司", Type: "domain", Value: "portal.example.com", Name: "门户",
		BusinessSystemCode: "PORTAL_001", BusinessSystemName: "门户系统", Environment: "production", NetworkScope: "public", NetworkAreaName: "上海办事处",
		Status: "active", Importance: "core", Source: "import", OwnerName: "张三", ExpiresAt: &expiresAt, CreatedAt: createdAt,
	}}, "csv")
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/csv; charset=utf-8" || fileName != "网络资产台账.csv" {
		t.Fatalf("unexpected metadata: %q %q", contentType, fileName)
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF")))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][2] != "域名" || rows[1][7] != "生产" || rows[1][8] != "公网" || rows[1][9] != "上海办事处" || rows[1][10] != "启用" || rows[1][11] != "核心" || rows[1][12] != "导入" {
		t.Fatalf("unexpected export row: %#v", rows)
	}
	if rows[0][13] != "责任人账号" || rows[0][16] != "有效期至" || rows[1][16] != "2027-12-31 23:59:59+08:00" || rows[1][17] != "2026-07-22 10:30:00+08:00" {
		t.Fatalf("unexpected exported expiry/times: %#v", rows[1])
	}
}

func TestBuildAssetExportXLSXAndRejectInvalidFormat(t *testing.T) {
	data, _, fileName, err := buildExport([]Asset{{OrganizationCode: "SUB_A", OrganizationName: "甲公司", Type: "ip", Value: "192.0.2.10"}}, "xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if fileName != "网络资产台账.xlsx" {
		t.Fatalf("unexpected file name: %q", fileName)
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
	if len(rows) != 2 || rows[0][0] != "所属组织编码" || rows[1][2] != "IP 地址" {
		t.Fatalf("unexpected XLSX rows: %#v", rows)
	}
	if _, _, _, err := buildExport(nil, "xls"); err != ErrInvalid {
		t.Fatalf("buildExport(xls) error = %v", err)
	}
}

func TestAssetFieldChangesUsesReadableValues(t *testing.T) {
	current := Asset{
		Type: "domain", Value: "old.example.com", Name: "旧门户", OrganizationName: "甲公司",
		BusinessSystemName: "门户系统", OwnerName: "张三", Environment: "production", NetworkScope: "public",
		NetworkAreaName: "总部", Status: "active", Importance: "normal", Notes: "旧备注",
	}
	input := CreateInput{
		Type: "url", Value: "https://portal.example.com", Name: "新门户", OrganizationID: "org-b", BusinessSystemID: "system-b",
		OwnerName: "李四", Environment: "testing", NetworkScope: "private", NetworkAreaID: "area-b", Status: "archived", Importance: "critical", Notes: "",
	}
	changes := assetFieldChanges(current, input, "乙公司", "新门户系统", "上海办事处", "", "")
	if len(changes) != 12 {
		t.Fatalf("changes count = %d, want 12: %#v", len(changes), changes)
	}
	if changes[0].Label != "资产类型" || changes[0].Before != "域名" || changes[0].After != "URL" {
		t.Fatalf("type change = %#v", changes[0])
	}
	if changes[6].Before != "生产" || changes[6].After != "测试" {
		t.Fatalf("environment change = %#v", changes[6])
	}
	if changes[11].Before != "旧备注" || changes[11].After != "未填写" {
		t.Fatalf("notes change = %#v", changes[11])
	}
}

func TestAssetFieldChangesIncludesAccountableUser(t *testing.T) {
	current := Asset{
		Type: "domain", Value: "portal.example.com", OrganizationName: "甲公司",
		Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal",
		OwnerUserID: "user-a", OwnerUsername: "zhangsan", OwnerUserDisplayName: "张三",
	}
	input := CreateInput{
		Type: "domain", Value: "portal.example.com", Environment: "production", NetworkScope: "public",
		Status: "active", Importance: "normal", OwnerUserID: "user-b",
	}
	changes := assetFieldChanges(current, input, "甲公司", "", "", "lisi", "李四")
	if len(changes) != 1 {
		t.Fatalf("changes = %#v, want one accountable-user change", changes)
	}
	if changes[0].Field != "owner_user_id" || changes[0].Before != "张三（zhangsan）" || changes[0].After != "李四（lisi）" {
		t.Fatalf("owner change = %#v", changes[0])
	}
}

func TestAssetFieldChangesSkipsUnchangedFields(t *testing.T) {
	current := Asset{Type: "domain", Value: "portal.example.com", OrganizationName: "甲公司", Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal"}
	input := CreateInput{Type: "domain", Value: "portal.example.com", Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal"}
	if changes := assetFieldChanges(current, input, "甲公司", "", "", "", ""); len(changes) != 0 {
		t.Fatalf("changes = %#v, want empty", changes)
	}
}

func TestNormalizeBatchUpdateInputSupportsStatusAndClearingNetworkArea(t *testing.T) {
	status := " ARCHIVED "
	clearArea := " "
	ids, normalizedStatus, normalizedArea, err := normalizeBatchUpdateInput(BatchUpdateInput{
		IDs: []string{" asset-a ", "asset-b", "asset-a"}, Status: &status, NetworkAreaID: &clearArea,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "asset-a" || ids[1] != "asset-b" {
		t.Fatalf("unexpected ids: %#v", ids)
	}
	if normalizedStatus == nil || *normalizedStatus != "archived" {
		t.Fatalf("unexpected status: %#v", normalizedStatus)
	}
	if normalizedArea == nil || *normalizedArea != "" {
		t.Fatalf("unexpected network area: %#v", normalizedArea)
	}
}

func TestNormalizeBatchUpdateInputRejectsMissingOrInvalidChanges(t *testing.T) {
	if _, _, _, err := normalizeBatchUpdateInput(BatchUpdateInput{IDs: []string{"asset-a"}}); err != ErrInvalid {
		t.Fatalf("missing changes error = %v, want ErrInvalid", err)
	}
	status := "deleted"
	if _, _, _, err := normalizeBatchUpdateInput(BatchUpdateInput{IDs: []string{"asset-a"}, Status: &status}); err != ErrInvalid {
		t.Fatalf("invalid status error = %v, want ErrInvalid", err)
	}
}

func TestCreateInputFromAssetPreservesEditableFields(t *testing.T) {
	customValues := []customfield.Value{{FieldKey: "data_level", Value: "important"}}
	item := Asset{
		Type: "url", Value: "https://portal.example.com", Name: "门户", OrganizationID: "org-a",
		BusinessSystemID: "system-a", Environment: "production", NetworkScope: "public", NetworkAreaID: "area-a",
		Status: "active", Importance: "core", OwnerUserID: "user-a", OwnerName: "张三", Notes: "备注", ExpiresAt: timePtr(time.Date(2027, 12, 31, 15, 59, 59, 0, time.UTC)), CustomValues: customValues,
	}
	input := createInputFromAsset(item)
	if input.Type != item.Type || input.Value != item.Value || input.OrganizationID != item.OrganizationID || input.NetworkAreaID != item.NetworkAreaID || input.Status != item.Status || input.OwnerUserID != item.OwnerUserID || input.Notes != item.Notes || input.ExpiresAt != item.ExpiresAt || len(input.CustomValues) != 1 || input.CustomValues[0].FieldKey != "data_level" {
		t.Fatalf("unexpected input: %#v", input)
	}
}

func TestAssetBatchErrorForTaskBoundDraft(t *testing.T) {
	code, message := assetBatchError(ErrTaskBound)
	if code != "task_bound" || message != "该资产已纳入填报任务，请在对应任务内处理" {
		t.Fatalf("assetBatchError(ErrTaskBound) = %q, %q", code, message)
	}
}

func TestAssetBatchErrorPreservesConcurrencySemantics(t *testing.T) {
	tests := []struct {
		err  error
		code string
	}{
		{ErrVersionRequired, "expected_version_required"},
		{ErrEditConflict, "asset_edit_conflict"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			code, _ := assetBatchError(tt.err)
			if code != tt.code {
				t.Fatalf("assetBatchError(%v) code = %q, want %q", tt.err, code, tt.code)
			}
		})
	}
}

func TestSetImportTargetExpectedVersionOnlySetsDraftVersion(t *testing.T) {
	input := CreateInput{}
	setImportTargetExpectedVersion(importUpdateTarget{Kind: "formal", Version: 7}, &input)
	if input.ExpectedVersion != nil {
		t.Fatal("formal import target unexpectedly received a draft expected_version")
	}
	setImportTargetExpectedVersion(importUpdateTarget{Kind: "draft", Version: 12}, &input)
	if input.ExpectedVersion == nil || *input.ExpectedVersion != 12 {
		t.Fatalf("draft import target expected_version = %v, want 12", input.ExpectedVersion)
	}
}

func TestParseImportExpiry(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		want      time.Time
		wantNil   bool
		wantError bool
	}{
		{name: "empty", value: "", wantNil: true},
		{name: "date uses end of day UTC plus eight", value: "2027-12-31", want: time.Date(2027, 12, 31, 23, 59, 59, 0, time.FixedZone("UTC+8", 8*60*60))},
		{name: "local date time uses UTC plus eight", value: "2027-12-31 16:30:45", want: time.Date(2027, 12, 31, 16, 30, 45, 0, time.FixedZone("UTC+8", 8*60*60))},
		{name: "RFC3339", value: "2027-12-31T16:00:00+08:00", want: time.Date(2027, 12, 31, 16, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))},
		{name: "invalid", value: "2027/12/31", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseImportExpiry(tt.value)
			if tt.wantError {
				if err == nil {
					t.Fatal("parseImportExpiry() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("parseImportExpiry() = %v, want nil", got)
				}
				return
			}
			if got == nil || !got.Equal(tt.want) {
				t.Fatalf("parseImportExpiry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func timePtr(value time.Time) *time.Time { return &value }

func TestBuildAssetExportRejectsOversizedSynchronousXLSX(t *testing.T) {
	items := make([]Asset, exportfile.MaxSynchronousXLSXRows+1)
	if _, _, _, err := buildExport(items, "xlsx"); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("buildExport oversized xlsx error = %v, want ErrExportTooLarge", err)
	}
}

func TestResolveAssetImportHeadersSupportsExplicitAndCustomMapping(t *testing.T) {
	definitions := []customfield.Definition{{FieldKey: "security_level", FieldName: "安全等级", IsRequired: true}}
	indexes, headers, err := resolveAssetImportHeaders([]string{"主机类别", "目标地址", "分级"}, ColumnMapping{
		"type": 0, "value": 1, "custom:security_level": 2,
	}, definitions)
	if err != nil {
		t.Fatal(err)
	}
	if indexes["type"] != 0 || indexes["value"] != 1 || headers[2] != "custom:security_level" {
		t.Fatalf("unexpected mapping: %#v %#v", indexes, headers)
	}
}

func TestResolveAssetImportHeadersRejectsMissingOrDuplicateRequiredMapping(t *testing.T) {
	if _, _, err := resolveAssetImportHeaders([]string{"a", "b"}, ColumnMapping{"type": 0}, nil); err == nil {
		t.Fatal("expected missing asset value mapping to fail")
	}
	if _, _, err := resolveAssetImportHeaders([]string{"a", "b"}, ColumnMapping{"type": 0, "value": 0}, nil); err == nil {
		t.Fatal("expected duplicate source column mapping to fail")
	}
}

func TestNormalizeIPAcceptsHostPort(t *testing.T) {
	// A scan export records one endpoint per listening port; every one of them
	// describes the same host and must collapse onto one asset.
	cases := map[string]string{
		"192.168.1.10:443":   "192.168.1.10",
		"192.168.1.10:80":    "192.168.1.10",
		"192.168.1.10":       "192.168.1.10",
		"[2001:db8::1]:8080": "2001:db8::1",
		"2001:db8::1":        "2001:db8::1",
	}
	for input, want := range cases {
		got, err := NormalizeValue("ip", input)
		if err != nil {
			t.Fatalf("NormalizeValue(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeValue(%q) = %q, want %q", input, got, want)
		}
	}
	for _, invalid := range []string{"192.168.1.10:", "192.168.1.10:99999", "192.168.1.10:http", "not-an-ip:80"} {
		if _, err := NormalizeValue("ip", invalid); err == nil {
			t.Fatalf("NormalizeValue(%q) should have failed", invalid)
		}
	}
}

func TestSplitHostPort(t *testing.T) {
	host, port, ok := SplitHostPort("192.168.1.10:443")
	if !ok || host != "192.168.1.10" || port != "443" {
		t.Fatalf("SplitHostPort returned (%q, %q, %v)", host, port, ok)
	}
	if _, _, ok := SplitHostPort("192.168.1.10"); ok {
		t.Fatal("a bare address carries no port")
	}
	if _, _, ok := SplitHostPort("192.168.1.10:99999"); ok {
		t.Fatal("an out-of-range port must be rejected")
	}
}

// TestTemplateExampleRowIsNotImportedAsAsset locks the reported bug: the sample
// row shipped inside the download template used to be previewed and written as a
// genuine asset ("domain / portal.example.com / 门户网站域名"), which is how
// placeholder data reached the real asset ledger.
func TestTemplateExampleRowIsNotImportedAsAsset(t *testing.T) {
	templateBytes, _, templateName, err := buildTemplate("xlsx", nil)
	if err != nil {
		t.Fatalf("build template: %v", err)
	}
	rows, _, err := readImportRows(templateName, bytes.NewReader(templateBytes))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("template has %d rows, want header plus sample row", len(rows))
	}
	headers, _, err := resolveAssetImportHeaders(rows[0], nil, nil)
	if err != nil {
		t.Fatalf("resolve headers: %v", err)
	}
	if !isAssetTemplateExample(rows[1], headers) {
		t.Fatalf("template row %v is not recognized as the shipped sample row", rows[1])
	}
	if got := countAssetImportDataRows(rows, headers); got != len(rows)-2 {
		t.Fatalf("data row count = %d, want %d (sample row must not be counted)", got, len(rows)-2)
	}

	// A user who edits any identifying cell has turned the sample into real data;
	// it must still be imported instead of being swallowed by the fix.
	edited := append([]string(nil), rows[1]...)
	edited[headers["value"]] = "portal.internal.example.com"
	if isAssetTemplateExample(edited, headers) {
		t.Fatal("an edited sample row must be treated as real data")
	}
	if got := countAssetImportDataRows(append(rows[:1:1], append([][]string{edited}, rows[2:]...)...), headers); got != len(rows)-1 {
		t.Fatalf("edited sample row count = %d, want %d", got, len(rows)-1)
	}
}

func TestAnalyzeImportColumnsIgnoresTemplateSampleRow(t *testing.T) {
	templateBytes, _, templateName, err := buildTemplate("xlsx", nil)
	if err != nil {
		t.Fatalf("build template: %v", err)
	}
	rows, _, err := readImportRows(templateName, bytes.NewReader(templateBytes))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	rows = append(rows, []string{"domain", "portal.corp.example.com", "集团门户", "", "", "", "", "", "", "", "", "", "", "", ""})
	analysis, err := importmapping.Analyze(dropAssetTemplateExampleRows(rows, nil), assetImportFields(nil))
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	for _, column := range analysis.Columns {
		for _, example := range column.Examples {
			if example == "portal.example.com" || example == "门户网站域名" {
				t.Fatalf("column %d example %q still exposes the shipped template sample", column.Index, example)
			}
		}
	}
}
