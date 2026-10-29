package organization

import (
	"context"
	"errors"
	"netassets/internal/auth"
	"testing"
)

func TestInvalidContactRejectedBeforeDatabaseAccess(t *testing.T) {
	s := &Service{}
	principal := auth.Principal{Roles: []string{"group_admin"}}
	for _, phone := range []string{"not-a-phone", "错误电话", "1380000000"} {
		_, err := s.Create(context.Background(), principal, CreateInput{
			ParentID: "11111111-1111-4111-8111-111111111111",
			Code:     "TEST_ORG",
			Name:     "名称",
			Type:     "department",
			Contact:  phone,
		}, "", "")
		if !errors.Is(err, ErrInvalidContact) || !errors.Is(err, ErrInvalid) {
			t.Fatalf("Create(%q): %v", phone, err)
		}
		_, err = s.Update(context.Background(), principal, "11111111-1111-4111-8111-111111111111", UpdateInput{Contact: phone}, "", "")
		if !errors.Is(err, ErrInvalidContact) {
			t.Fatalf("Update(%q): %v", phone, err)
		}
	}
}
