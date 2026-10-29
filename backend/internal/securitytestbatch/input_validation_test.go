package securitytestbatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"netassets/internal/auth"
)

func validFieldTestInput() Input {
	start := time.Date(2020, 1, 1, 8, 0, 0, 0, time.UTC)
	return Input{Name: "历史批次", TestType: "manual", Provider: "测试单位", AuthorizationNote: "授权依据", StartAt: start, EndAt: start.Add(time.Hour), AssetIDs: []string{"11111111-1111-4111-8111-111111111111"}}
}

func TestInputFieldValidationBeforeDatabase(t *testing.T) {
	cases := []struct {
		name, message string
		change        func(*Input)
	}{
		{"name-required", "批次名称不能为空", func(in *Input) { in.Name = " \t　" }},
		{"name-long", "批次名称最多 255 字", func(in *Input) { in.Name = strings.Repeat("名", 256) }},
		{"provider-required", "测试单位不能为空", func(in *Input) { in.Provider = "　 " }},
		{"provider-long", "测试单位最多 255 字", func(in *Input) { in.Provider = strings.Repeat("P", 256) }},
		{"authorization-required", "授权说明不能为空", func(in *Input) { in.AuthorizationNote = " \n" }},
		{"authorization-long", "授权说明最多 20000 字", func(in *Input) { in.AuthorizationNote = strings.Repeat("授", 20001) }},
		{"notes-long", "测试约束最多 20000 字", func(in *Input) { in.Notes = strings.Repeat("😀", 20001) }},
		{"type", "请选择有效的测试类型", func(in *Input) { in.TestType = "invalid" }},
		{"start", "请选择计划开始时间", func(in *Input) { in.StartAt = time.Time{} }},
		{"end", "请选择计划结束时间", func(in *Input) { in.EndAt = time.Time{} }},
		{"end-equal", "计划结束时间必须晚于计划开始时间", func(in *Input) { in.EndAt = in.StartAt }},
		{"end-before", "计划结束时间必须晚于计划开始时间", func(in *Input) { in.EndAt = in.StartAt.Add(-time.Hour) }},
		{"scope-empty", "请至少选择 1 项公网资产", func(in *Input) { in.AssetIDs = []string{" ", ""} }},
		{"scope-invalid", "公网资产范围包含无效的资产标识", func(in *Input) { in.AssetIDs = []string{"invalid"} }},
		{"scope-long", "公网资产范围最多 200 项", func(in *Input) {
			in.AssetIDs = nil
			for i := 0; i < 201; i++ {
				in.AssetIDs = append(in.AssetIDs, fmt.Sprintf("11111111-1111-4111-8111-%012d", i))
			}
		}},
	}
	svc := NewService(nil) // invalid input must return before starting a transaction
	for _, tc := range cases {
		for _, role := range []string{auth.RoleGroupAdmin, auth.RoleSecurityOperator, "organization_admin"} {
			for _, action := range []string{"create", "update"} {
				t.Run(tc.name+"/"+role+"/"+action, func(t *testing.T) {
					in := validFieldTestInput()
					tc.change(&in)
					principal := auth.Principal{Roles: []string{role}, Permissions: []string{auth.PermissionSecurityTest}}
					var err error
					if action == "create" {
						_, err = svc.Create(context.Background(), principal, in, "", "")
					} else {
						_, err = svc.Update(context.Background(), principal, "11111111-1111-4111-8111-111111111111", in, "", "")
					}
					if role == "organization_admin" {
						if !errors.Is(err, ErrForbidden) {
							t.Fatalf("want forbidden before field validation, got %v", err)
						}
						return
					}
					assertCommentValidation(t, err, tc.message)
				})
			}
		}
	}
}

func TestInputUnicodeBoundariesAndHistoricalDates(t *testing.T) {
	for _, char := range []string{"中", "T", "😀"} {
		for _, field := range []struct {
			name  string
			limit int
			set   func(*Input, string)
		}{
			{"name", 255, func(in *Input, v string) { in.Name = v }},
			{"provider", 255, func(in *Input, v string) { in.Provider = v }},
			{"authorization", 20000, func(in *Input, v string) { in.AuthorizationNote = v }},
			{"notes", 20000, func(in *Input, v string) { in.Notes = v }},
		} {
			t.Run(field.name+"/"+char, func(t *testing.T) {
				in := validFieldTestInput()
				field.set(&in, " \t"+strings.Repeat(char, field.limit)+"　\n")
				normalizeInput(&in)
				if !validInput(in) {
					t.Fatal("exact character boundary or historical dates rejected")
				}
				field.set(&in, strings.Repeat(char, field.limit+1))
				if validInput(in) {
					t.Fatal("over-limit input accepted")
				}
			})
		}
	}
	in := validFieldTestInput()
	for i := 0; i < 201; i++ {
		in.AssetIDs = append(in.AssetIDs, in.AssetIDs[0])
	}
	normalizeInput(&in)
	if !validInput(in) || len(in.AssetIDs) != 1 {
		t.Fatal("deduplicated scope compatibility changed")
	}
}
