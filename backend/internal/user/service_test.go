package user

import (
	"errors"
	"testing"
)

func TestNormalizeAppointmentsAllowsGlobalRoleOnlyAccount(t *testing.T) {
	items := []AppointmentInput(nil)
	if err := normalizeAppointments(&items, true); err != nil {
		t.Fatalf("normalizeAppointments(global-only) error = %v", err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("normalized global-only appointments = %#v, want empty non-nil slice", items)
	}
}

func TestNormalizeAppointmentsRejectsRolelessAccount(t *testing.T) {
	items := []AppointmentInput(nil)
	if err := normalizeAppointments(&items, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("normalizeAppointments(roleless) error = %v, want ErrInvalid", err)
	}
}
