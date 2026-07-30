package dataquality

import (
	"testing"
	"time"
)

func TestAutoScanScheduleDueDate(t *testing.T) {
	schedule, err := NewAutoScanSchedule(true, 2, 30, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("NewAutoScanSchedule() error = %v", err)
	}

	before := time.Date(2026, time.July, 30, 2, 29, 0, 0, schedule.location)
	if date, due := schedule.dueDate(before); due || date != "" {
		t.Fatalf("dueDate(before) = %q, %v; want not due", date, due)
	}
	after := time.Date(2026, time.July, 30, 2, 30, 0, 0, schedule.location)
	if date, due := schedule.dueDate(after); !due || date != "2026-07-30" {
		t.Fatalf("dueDate(after) = %q, %v; want 2026-07-30, true", date, due)
	}
}

func TestAutoScanScheduleUsesConfiguredTimezone(t *testing.T) {
	schedule, err := NewAutoScanSchedule(true, 0, 15, "Asia/Shanghai")
	if err != nil {
		t.Fatalf("NewAutoScanSchedule() error = %v", err)
	}
	utcNow := time.Date(2026, time.July, 29, 16, 15, 0, 0, time.UTC)
	if date, due := schedule.dueDate(utcNow); !due || date != "2026-07-30" {
		t.Fatalf("dueDate(utcNow) = %q, %v; want Shanghai date 2026-07-30", date, due)
	}
}

func TestNewAutoScanScheduleRejectsInvalidValues(t *testing.T) {
	if _, err := NewAutoScanSchedule(true, 24, 0, "Asia/Shanghai"); err == nil {
		t.Fatal("NewAutoScanSchedule() error = nil, want invalid hour")
	}
	if _, err := NewAutoScanSchedule(true, 2, 0, "Mars/Olympus"); err == nil {
		t.Fatal("NewAutoScanSchedule() error = nil, want invalid timezone")
	}
}
