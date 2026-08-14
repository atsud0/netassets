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
