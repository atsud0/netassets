package asset

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

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
	headers := mapHeaders([]string{"\ufeff资产类型", "资产值", "资产名称", "业务系统编码", "环境", "网络范围", "重要程度", "负责人", "备注"})
	input := importInput([]string{"域名", "Portal.Example.com", "门户", "SYS_001", "生产", "公网", "核心", "张三", "测试资产"}, headers, "org-id")
	if input.Type != "domain" || input.Environment != "production" || input.NetworkScope != "public" || input.Importance != "core" {
		t.Fatalf("unexpected translated input: %+v", input)
	}
	if input.BusinessSystemID != "SYS_001" || input.OrganizationID != "org-id" {
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
	if len(rows) != 2 || rows[0][0] != "资产类型" || rows[0][1] != "资产值" {
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
	if len(rows) != 2 || rows[0][0] != "资产类型" || rows[0][1] != "资产值" {
		t.Fatalf("unexpected XLSX template: %#v", rows)
	}
}
