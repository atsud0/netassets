package inventorytask

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"netassets/internal/auth"
)

func TestInventoryTaskValidationBoundariesAndNormalization(t *testing.T) {
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	unique := func(n int) []string {
		values := make([]string, n)
		for i := range values {
			values[i] = fmt.Sprintf("value-%d", i)
		}
		return values
	}
	valid := func() CreateInput {
		return CreateInput{Name: "填报", StartAt: start, DeadlineAt: start.Add(time.Hour), OrganizationIDs: []string{"org-1"}, RequiredFields: []string{"network_assets"}}
	}
	for _, char := range []string{"填", "T", "😀"} {
		t.Run(char, func(t *testing.T) {
			in := valid()
			in.Name = "　" + strings.Repeat(char, 255) + " "
			in.Description = " " + strings.Repeat(char, 5000) + "　"
			in.OrganizationIDs = unique(1000)
			in.OrganizationIDs = append(in.OrganizationIDs, " value-0 ", " ")
			in.RequiredFields = []string{" business_systems ", "data_security", "network_assets", "owner_contacts", "network_assets", " "}
			in.QualityRules = unique(100)
			in.QualityRules = append(in.QualityRules, "value-0", "　")
			if err := normalizeCreateInput(&in); err != nil {
				t.Fatal(err)
			}
			if in.Name != strings.Repeat(char, 255) || in.Description != strings.Repeat(char, 5000) || in.Status != "draft" || in.AssetNetworkScope != "all" || len(in.OrganizationIDs) != 1000 || len(in.RequiredFields) != 4 || len(in.QualityRules) != 100 {
				t.Fatal("normalization changed accepted boundary")
			}
			in.Name = strings.Repeat(char, 256)
			if err := normalizeCreateInput(&in); !errors.Is(err, ErrInvalid) || err.Error() != "任务名称最多 255 字" {
				t.Fatalf("name: %v", err)
			}
			in.Name = "填报"
			in.Description = strings.Repeat(char, 5001)
			if err := normalizeCreateInput(&in); !errors.Is(err, ErrInvalid) || err.Error() != "任务说明最多 5000 字" {
				t.Fatalf("description: %v", err)
			}
		})
	}
	for _, status := range []string{"draft", "active"} {
		for _, scope := range []string{"all", "public", "private"} {
			in := valid()
			in.Status = " " + status + " "
			in.AssetNetworkScope = " " + scope + " "
			if err := normalizeCreateInput(&in); err != nil || in.Status != status || in.AssetNetworkScope != scope {
				t.Fatalf("status/scope: %v", err)
			}
		}
	}
	for _, field := range []string{"business_systems", "data_security", "network_assets"} {
		in := ScopePreviewInput{OrganizationIDs: unique(1000), RequiredFields: []string{field, "owner_contacts", field}, AssetNetworkScope: " "}
		if err := normalizeScopePreviewInput(&in); err != nil || in.AssetNetworkScope != "all" || len(in.RequiredFields) != 2 {
			t.Fatalf("preview %s: %v", field, err)
		}
	}
}

func TestInventoryTaskInputPermissionPrecedesValidation(t *testing.T) {
	for _, role := range []string{"organization_admin", "security_operator", "asset_operator", "remediation_executor"} {
		t.Run(role, func(t *testing.T) {
			service := NewService(nil)
			p := auth.Principal{Roles: []string{role}}
			if _, err := service.Create(context.Background(), p, CreateInput{}, "", ""); !errors.Is(err, ErrForbidden) {
				t.Fatalf("create: %v", err)
			}
			if _, err := service.PreviewScope(context.Background(), p, ScopePreviewInput{}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("preview: %v", err)
			}
		})
	}
}
