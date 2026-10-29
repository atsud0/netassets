package organization

import (
	"context"
	"errors"
	"strings"
	"testing"

	"netassets/internal/auth"
)

func TestBasicInfoCharacterBoundaries(t *testing.T) {
	principal := auth.Principal{Roles: []string{"group_admin"}}
	for _, letter := range []string{"A", "组", "𠮷"} {
		t.Run(letter, func(t *testing.T) {
			if err := validateBasicInfo(strings.Repeat(letter, 255), strings.Repeat(letter, 128), strings.Repeat(letter, 128), strings.Repeat(letter, 5000)); err != nil {
				t.Fatal(err)
			}
			if err := validateBasicInfo("名称", "", "", ""); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				field string
				limit int
				label string
			}{
				{"name", 255, "组织名称"}, {"short_name", 128, "组织简称"}, {"manager", 128, "负责人"}, {"notes", 5000, "备注"},
			} {
				t.Run(tc.field, func(t *testing.T) {
					in := UpdateInput{Name: "名称"}
					over := strings.Repeat(letter, tc.limit+1)
					switch tc.field {
					case "name":
						in.Name = over
					case "short_name":
						in.ShortName = over
					case "manager":
						in.Manager = over
					case "notes":
						in.Notes = over
					}
					assertOrganizationField(t, validateBasicInfo(in.Name, in.ShortName, in.Manager, in.Notes), tc.label)
					// A nil database deliberately proves both public entry points reject
					// invalid data before any persistence or authorization database calls.
					s := &Service{}
					_, err := s.Create(context.Background(), principal, CreateInput{ParentID: "11111111-1111-4111-8111-111111111111", Code: "TEST_ORG", Type: "department", Name: in.Name, ShortName: in.ShortName, Manager: in.Manager, Notes: in.Notes, Contact: "13800000000"}, "", "")
					assertOrganizationField(t, err, tc.label)
					_, err = s.Update(context.Background(), principal, "11111111-1111-4111-8111-111111111111", in, "", "")
					assertOrganizationField(t, err, tc.label)
				})
			}
		})
	}
}

func TestOrganizationEmptyNamesAndCreateFields(t *testing.T) {
	s := &Service{}
	principal := auth.Principal{Roles: []string{"group_admin"}}
	for _, name := range []string{"", " \t\n　"} {
		in := CreateInput{Code: "OK", Name: name, ParentID: "11111111-1111-4111-8111-111111111111", Type: "department", Contact: "13800000000"}
		_, err := s.Create(context.Background(), principal, in, "", "")
		assertOrganizationField(t, err, "组织名称")
		_, err = s.Update(context.Background(), principal, in.ParentID, UpdateInput{Name: name}, "", "")
		assertOrganizationField(t, err, "组织名称")
	}
	for _, tc := range []struct{ code, parent, kind, label string }{
		{strings.Repeat("A", 65), "11111111-1111-4111-8111-111111111111", "department", "组织编码"},
		{"_BAD", "11111111-1111-4111-8111-111111111111", "department", "组织编码"},
		{"OK", "invalid", "department", "上级组织"},
		{"OK", "11111111-1111-4111-8111-111111111111", "group", "组织类型"},
	} {
		_, err := s.Create(context.Background(), principal, CreateInput{Code: tc.code, Name: "名称", ParentID: tc.parent, Type: tc.kind, Contact: "13800000000"}, "", "")
		assertOrganizationField(t, err, tc.label)
	}
}

func assertOrganizationField(t *testing.T, err error, label string) {
	t.Helper()
	var validation *ValidationError
	if !errors.Is(err, ErrInvalid) || !errors.As(err, &validation) || !strings.Contains(validation.Message, label) {
		t.Fatalf("expected invalid %s field, got %v", label, err)
	}
}
