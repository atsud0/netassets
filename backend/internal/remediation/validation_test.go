package remediation

import (
	"context"
	"errors"
	"netassets/internal/auth"
	"strings"
	"testing"
	"time"
)

func TestFieldValidationBeforeDatabaseAccess(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	p := auth.Principal{}
	assertField := func(err error, field string) {
		t.Helper()
		var validation *ValidationError
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &validation) || !strings.Contains(validation.Message, field) {
			t.Fatalf("expected %s validation, got %v", field, err)
		}
	}
	_, err := s.SaveVendor(ctx, p, "", "", "厂商", "abc", "enabled")
	assertField(err, "联系方式")
	_, err = s.SaveVendor(ctx, p, "", "", "", "", "enabled")
	assertField(err, "厂商名称")
	_, err = s.SaveVendor(ctx, p, "", "", "厂商", "13800000000", "enabled")
	assertField(err, "合作组织")
	_, err = s.SaveCatalog(ctx, p, "", CatalogInput{Name: ""})
	assertField(err, "统一厂商名称")
	_, err = s.SaveCatalog(ctx, p, "", CatalogInput{Name: "厂商", Reason: ""})
	assertField(err, "变更原因")
	_, err = s.SaveCatalog(ctx, p, "", CatalogInput{Name: strings.Repeat("厂", 256), Reason: "原因"})
	assertField(err, "统一厂商名称")
	assertField(s.UpdateGrant(ctx, p, "", "enabled", time.Time{}, ""), "变更原因")
	for _, reason := range []string{"", strings.Repeat("因", 2001), strings.Repeat("R", 2001)} {
		assertField(s.UpdateGrant(ctx, p, "", "revoked", time.Time{}, reason), "撤销原因")
		assertField(s.RevokeInvitation(ctx, p, "", reason), "撤销原因")
	}
	assertField(s.RevokeInvitation(ctx, p, "", strings.Repeat("因", 2001)), "撤销原因")
}
