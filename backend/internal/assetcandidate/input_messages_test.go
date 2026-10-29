package assetcandidate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"netassets/internal/asset"
	"netassets/internal/auth"
)

func TestCandidateCreateReportsSpecificInputBeforeDatabase(t *testing.T) {
	group := auth.Principal{Roles: []string{"group_admin"}}
	for _, tc := range []struct {
		name, message string
		change        func(*CreateInput)
	}{
		{"source", "请选择有效的发现来源", func(in *CreateInput) { in.Source = "bad" }},
		{"type", "请选择有效的资产类型", func(in *CreateInput) { in.AssetType = "bad" }},
		{"blank value", "资产值不能为空", func(in *CreateInput) { in.AssetValue = "　 " }},
		{"value", "资产值与所选资产类型不匹配，请检查资产值", func(in *CreateInput) { in.AssetValue = "not-a-domain" }},
		{"organization", "请选择建议所属组织", func(in *CreateInput) { in.OrganizationID = "" }},
		{"organization format", "请选择有效的建议所属组织", func(in *CreateInput) { in.OrganizationID = "bad" }},
		{"name", "资产名称最多 255 字", func(in *CreateInput) { in.Name = strings.Repeat("中", 256) }},
		{"source reference", "来源参考最多 255 字", func(in *CreateInput) { in.SourceReference = strings.Repeat("a", 256) }},
		{"notes", "备注最多 5000 字", func(in *CreateInput) { in.Notes = strings.Repeat("🙂", 5001) }},
		{"suggested data", "请检查建议资产信息的格式", func(in *CreateInput) { in.SuggestedData = map[string]any{"secret": make(chan int)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := CreateInput{AssetType: "domain", AssetValue: "candidate.example.com", OrganizationID: "f2901cdc-89a5-41f9-b3fd-1b9ee9f5534a", Source: "manual"}
			tc.change(&in)
			_, err := NewService(nil, nil).Create(context.Background(), group, in, "", "")
			if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
				t.Fatalf("input error = %v, want %q", err, tc.message)
			}
			_, err = NewService(nil, nil).Create(context.Background(), auth.Principal{}, in, "", "")
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("field validation leaked before permission: %v", err)
			}
		})
	}
}

func TestCandidateAssetFieldErrorsSurviveSingleAndBatchMapping(t *testing.T) {
	_, err := asset.PrepareCreateInput(&asset.CreateInput{OrganizationID: "f2901cdc-89a5-41f9-b3fd-1b9ee9f5534a", Type: "domain", Value: "candidate.example.com", Environment: "bad", NetworkScope: "public", Status: "active", Importance: "normal"})
	mapped := mapAssetError(err)
	if !errors.Is(mapped, ErrInvalid) || mapped.Error() != "请选择有效的部署环境" {
		t.Fatalf("asset field message lost: %v", mapped)
	}
	code, message := describeBatchError(mapped)
	if code != "invalid_asset_candidate" || message != "请选择有效的部署环境" {
		t.Fatalf("batch error: %s %s", code, message)
	}
	// Arbitrary wrapped invalid errors must never be sent to the client verbatim.
	code, message = describeBatchError(errors.Join(ErrInvalid, errors.New("internal database details")))
	if code != "invalid_asset_candidate" || strings.Contains(message, "database") {
		t.Fatalf("unsafe fallback %s %s", code, message)
	}
}
