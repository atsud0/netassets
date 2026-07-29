package asset

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

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
	headers := mapHeaders([]string{"\ufeff资产类型", "资产值", "资产名称", "业务系统编码", "环境", "网络暴露范围", "网络区域", "重要程度", "负责人", "备注"})
	input, err := importInput([]string{"域名", "Portal.Example.com", "门户", "SYS_001", "生产", "公网", "上海办事处", "核心", "张三", "测试资产"}, headers, "org-id")
	if err != nil {
		t.Fatal(err)
	}
	if input.Type != "domain" || input.Environment != "production" || input.NetworkScope != "public" || input.Importance != "core" {
		t.Fatalf("unexpected translated input: %+v", input)
	}
	if input.BusinessSystemID != "SYS_001" || input.NetworkAreaID != "上海办事处" || input.OrganizationID != "org-id" {
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
	if len(rows) != 2 || rows[0][0] != "资产类型" || rows[0][1] != "资产值" || rows[0][9] != "有效期至" || rows[1][9] != "2027-12-31" {
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
	if len(rows) != 2 || rows[0][0] != "资产类型" || rows[0][1] != "资产值" || rows[0][9] != "有效期至" || rows[1][9] != "2027-12-31" {
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
	if rows[0][15] != "有效期至" || rows[1][15] != "2027-12-31 23:59:59+08:00" || rows[1][16] != "2026-07-22 10:30:00+08:00" {
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
	changes := assetFieldChanges(current, input, "乙公司", "新门户系统", "上海办事处")
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

func TestAssetFieldChangesSkipsUnchangedFields(t *testing.T) {
	current := Asset{Type: "domain", Value: "portal.example.com", OrganizationName: "甲公司", Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal"}
	input := CreateInput{Type: "domain", Value: "portal.example.com", Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal"}
	if changes := assetFieldChanges(current, input, "甲公司", "", ""); len(changes) != 0 {
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
	item := Asset{
		Type: "url", Value: "https://portal.example.com", Name: "门户", OrganizationID: "org-a",
		BusinessSystemID: "system-a", Environment: "production", NetworkScope: "public", NetworkAreaID: "area-a",
		Status: "active", Importance: "core", OwnerName: "张三", Notes: "备注", ExpiresAt: timePtr(time.Date(2027, 12, 31, 15, 59, 59, 0, time.UTC)),
	}
	input := createInputFromAsset(item)
	if input.Type != item.Type || input.Value != item.Value || input.OrganizationID != item.OrganizationID || input.NetworkAreaID != item.NetworkAreaID || input.Status != item.Status || input.Notes != item.Notes || input.ExpiresAt != item.ExpiresAt {
		t.Fatalf("unexpected input: %#v", input)
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
