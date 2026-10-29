package securityworkorder

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"netassets/internal/auth"
)

const fieldFixtureUUID = "11111111-1111-4111-8111-111111111111"

func fieldFindingInput() FindingInput {
	return FindingInput{Title: "字段回归", Severity: "high", Status: "pending", AssigneeOrganizationID: fieldFixtureUUID, AssetID: fieldFixtureUUID, Description: "漏洞描述", Source: "manual"}
}
func fieldWorkOrderInput() WorkOrderInput {
	return WorkOrderInput{Title: "字段回归工单", AssigneeOrganizationID: fieldFixtureUUID, RemediationRequirement: "修复要求", DueAt: time.Now().Add(time.Hour), FindingIDs: []string{fieldFixtureUUID}}
}
func fieldManager() auth.Principal {
	return auth.Principal{Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{auth.PermissionSecurityFinding, auth.PermissionSecurityWorkOrder}}
}

func TestFindingCreationFieldMessages(t *testing.T) {
	cases := []struct {
		name, message string
		change        func(*FindingInput)
	}{
		{"empty title", "漏洞标题不能为空", func(i *FindingInput) { i.Title = "　 " }},
		{"long title", "漏洞标题最多 255 字", func(i *FindingInput) { i.Title = strings.Repeat("中", 256) }},
		{"severity", "请选择有效的严重等级", func(i *FindingInput) { i.Severity = "bad" }},
		{"status", "请选择有效的安全发现状态", func(i *FindingInput) { i.Status = "bad" }},
		{"organization", "请选择有效的责任组织", func(i *FindingInput) { i.AssigneeOrganizationID = "bad" }},
		{"resource required", "请至少关联一项网络资产或业务系统", func(i *FindingInput) { i.AssetID = "" }},
		{"asset", "关联网络资产标识不正确", func(i *FindingInput) { i.AssetID = "bad" }},
		{"system", "关联业务系统标识不正确", func(i *FindingInput) { i.BusinessSystemID = "bad" }},
		{"batch", "关联测试批次标识不正确", func(i *FindingInput) { i.TestBatchID = "bad" }},
		{"vulnerability type", "漏洞类型最多 128 字", func(i *FindingInput) { i.VulnerabilityType = strings.Repeat("🙂", 129) }},
		{"empty description", "漏洞描述不能为空", func(i *FindingInput) { i.Description = "　 " }},
		{"long description", "漏洞描述最多 20000 字", func(i *FindingInput) { i.Description = strings.Repeat("中", 20001) }},
		{"evidence", "漏洞证据最多 30000 字", func(i *FindingInput) { i.Evidence = strings.Repeat("中", 30001) }},
		{"advice", "修复建议最多 20000 字", func(i *FindingInput) { i.RemediationAdvice = strings.Repeat("中", 20001) }},
		{"source", "请选择有效的安全发现来源", func(i *FindingInput) { i.Source = "bad" }},
	}
	service := &Service{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := fieldFindingInput()
			tc.change(&input)
			_, err := service.CreateFinding(context.Background(), fieldManager(), input, "", "")
			if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
				t.Fatalf("got %v, want ErrInvalid with %q", err, tc.message)
			}
			_, err = service.CreateFinding(context.Background(), auth.Principal{}, input, "", "")
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("must deny before field details: %v", err)
			}
		})
	}
}

func TestWorkOrderCreationFieldMessages(t *testing.T) {
	cases := []struct {
		name, message string
		change        func(*WorkOrderInput)
	}{
		{"execution mode", "请选择有效的工单执行方式", func(i *WorkOrderInput) { i.ExecutionMode = "bad" }},
		{"empty title", "工单标题不能为空", func(i *WorkOrderInput) { i.Title = "　 " }},
		{"long title", "工单标题最多 255 字", func(i *WorkOrderInput) { i.Title = strings.Repeat("中", 256) }},
		{"organization", "请选择有效的责任组织", func(i *WorkOrderInput) { i.AssigneeOrganizationID = "bad" }},
		{"description", "工单说明最多 10000 字", func(i *WorkOrderInput) { i.Description = strings.Repeat("中", 10001) }},
		{"empty requirement", "整改要求不能为空", func(i *WorkOrderInput) { i.RemediationRequirement = "　 " }},
		{"long requirement", "整改要求最多 20000 字", func(i *WorkOrderInput) { i.RemediationRequirement = strings.Repeat("中", 20001) }},
		{"missing due date", "请选择整改截止时间", func(i *WorkOrderInput) { i.DueAt = time.Time{} }},
		{"past due date", "整改截止时间不能早于当前时间", func(i *WorkOrderInput) { i.DueAt = time.Now().Add(-time.Hour) }},
		{"missing findings", "请至少选择一条安全发现", func(i *WorkOrderInput) { i.FindingIDs = []string{"　 ", " "} }},
		{"too many findings", "单个工单最多关联 100 条安全发现", func(i *WorkOrderInput) {
			i.FindingIDs = nil
			for n := 0; n < 101; n++ {
				i.FindingIDs = append(i.FindingIDs, time.Unix(int64(n), 0).Format(time.RFC3339))
			}
		}},
		{"invalid finding", "关联安全发现标识不正确", func(i *WorkOrderInput) { i.FindingIDs = []string{"bad"} }},
	}
	service := &Service{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := fieldWorkOrderInput()
			tc.change(&input)
			_, err := service.CreateWorkOrder(context.Background(), fieldManager(), input, "", "")
			if !errors.Is(err, ErrInvalid) || err.Error() != tc.message {
				t.Fatalf("got %v, want ErrInvalid with %q", err, tc.message)
			}
			_, err = service.CreateWorkOrder(context.Background(), auth.Principal{}, input, "", "")
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("must deny before field details: %v", err)
			}
		})
	}
}

func TestSecurityCreateUnicodeBoundariesPreserved(t *testing.T) {
	for _, unit := range []string{"a", "中", "🙂"} {
		t.Run(unit, func(t *testing.T) {
			finding := fieldFindingInput()
			finding.Title = strings.Repeat(unit, 255)
			finding.VulnerabilityType = strings.Repeat(unit, 128)
			finding.Description = strings.Repeat(unit, 20000)
			finding.Evidence = strings.Repeat(unit, 30000)
			finding.RemediationAdvice = strings.Repeat(unit, 20000)
			if !validFindingInput(finding) {
				t.Fatal("valid Unicode full-length finding rejected")
			}
			order := fieldWorkOrderInput()
			order.Title = strings.Repeat(unit, 255)
			order.Description = strings.Repeat(unit, 10000)
			order.RemediationRequirement = strings.Repeat(unit, 20000)
			if !validWorkOrderInput(order) {
				t.Fatal("valid Unicode full-length work order rejected")
			}
		})
	}
}

func TestSecurityCreateValidationCompatibility(t *testing.T) {
	for _, unit := range []string{"a", "中", "🙂"} {
		for _, tc := range []struct {
			name  string
			limit int
			set   func(*FindingInput, string)
		}{
			{"title", 255, func(i *FindingInput, v string) { i.Title = v }},
			{"type", 128, func(i *FindingInput, v string) { i.VulnerabilityType = v }},
			{"description", 20000, func(i *FindingInput, v string) { i.Description = v }},
			{"evidence", 30000, func(i *FindingInput, v string) { i.Evidence = v }},
			{"advice", 20000, func(i *FindingInput, v string) { i.RemediationAdvice = v }},
		} {
			t.Run("finding/"+tc.name+"/"+unit, func(t *testing.T) {
				i := fieldFindingInput()
				tc.set(&i, strings.Repeat(unit, tc.limit+1))
				if err := validateFindingInput(i); !errors.Is(err, ErrInvalid) {
					t.Fatalf("over-limit accepted: %v", err)
				}
			})
		}
		for _, tc := range []struct {
			name  string
			limit int
			set   func(*WorkOrderInput, string)
		}{
			{"title", 255, func(i *WorkOrderInput, v string) { i.Title = v }},
			{"description", 10000, func(i *WorkOrderInput, v string) { i.Description = v }},
			{"requirement", 20000, func(i *WorkOrderInput, v string) { i.RemediationRequirement = v }},
		} {
			t.Run("order/"+tc.name+"/"+unit, func(t *testing.T) {
				i := fieldWorkOrderInput()
				tc.set(&i, strings.Repeat(unit, tc.limit+1))
				if err := validateWorkOrderInput(i); !errors.Is(err, ErrInvalid) {
					t.Fatalf("over-limit accepted: %v", err)
				}
			})
		}
	}
	input := fieldWorkOrderInput()
	input.DueAt = time.Now().Add(-30 * time.Second)
	if err := validateWorkOrderInput(input); err != nil {
		t.Fatalf("existing clock tolerance removed: %v", err)
	}
	input.DueAt = time.Now().Add(-2 * time.Minute)
	if err := validateWorkOrderInput(input); !errors.Is(err, ErrInvalid) {
		t.Fatalf("past due date accepted: %v", err)
	}
	input = fieldWorkOrderInput()
	input.FindingIDs = make([]string, 101)
	for n := range input.FindingIDs {
		input.FindingIDs[n] = " " + fieldFixtureUUID + " "
	}
	normalizeWorkOrderInput(&input)
	if len(input.FindingIDs) != 1 || validateWorkOrderInput(input) != nil {
		t.Fatal("duplicate ID normalization changed")
	}
	finding := fieldFindingInput()
	finding.Title = "　 合法标题　 "
	finding.Source = ""
	finding.Status = ""
	if err := prepareFindingCreateInput(fieldManager(), &finding); err != nil {
		t.Fatal(err)
	}
	if finding.Title != "合法标题" || finding.Source != "manual" || finding.Status != "pending" {
		t.Fatal("creation normalization changed")
	}
}
