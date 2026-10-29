package asset

import (
	"errors"
	"strings"
	"testing"
)

func TestPrepareAssetCreateInputFieldMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*CreateInput)
		message string
	}{
		{"organization", func(in *CreateInput) { in.OrganizationID = " " }, "请选择所属组织"},
		{"type", func(in *CreateInput) { in.Type = "bad" }, "请选择有效的资产类型"},
		{"blank value", func(in *CreateInput) { in.Value = "　 " }, "资产值不能为空"},
		{"invalid value", func(in *CreateInput) { in.Value = "not-a-domain" }, "资产值与所选资产类型不匹配，请检查资产值"},
		{"environment", func(in *CreateInput) { in.Environment = "bad" }, "请选择有效的部署环境"},
		{"network scope", func(in *CreateInput) { in.NetworkScope = "bad" }, "请选择有效的网络范围"},
		{"status", func(in *CreateInput) { in.Status = "bad" }, "请选择有效的资产状态"},
		{"name", func(in *CreateInput) { in.Name = strings.Repeat("🙂", 256) }, "资产名称最多 255 字"},
		{"owner name", func(in *CreateInput) { in.OwnerName = strings.Repeat("中", 129) }, "负责人最多 128 字"},
		{"importance", func(in *CreateInput) { in.Importance = "bad" }, "请选择有效的资产重要程度"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := CreateInput{Type: "domain", Value: "input.example.com", OrganizationID: "f2901cdc-89a5-41f9-b3fd-1b9ee9f5534a", Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal"}
			tc.change(&input)
			_, err := PrepareCreateInput(&input)
			if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
				t.Fatalf("error = %v, want %q", err, tc.message)
			}
		})
	}
}

func TestPrepareAssetCreateInputUnicodeTextBoundaries(t *testing.T) {
	for _, unit := range []string{"a", "中", "🙂"} {
		in := CreateInput{Type: "domain", Value: "input.example.com", OrganizationID: "f2901cdc-89a5-41f9-b3fd-1b9ee9f5534a", Name: strings.Repeat(unit, 255), OwnerName: strings.Repeat(unit, 128)}
		if _, err := PrepareCreateInput(&in); err != nil {
			t.Fatalf("valid %s boundaries: %v", unit, err)
		}
		if in.Name != strings.Repeat(unit, 255) || in.OwnerName != strings.Repeat(unit, 128) {
			t.Fatal("valid text changed")
		}
	}
}
