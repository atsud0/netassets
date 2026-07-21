package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("APP_ADDR", ":9090")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("SESSION_TTL", "24h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Address != ":9090" {
		t.Fatalf("Address = %q, want %q", cfg.Address, ":9090")
	}
	if cfg.SessionTTL.Hours() != 24 {
		t.Fatalf("SessionTTL = %v, want 24h", cfg.SessionTTL)
	}
}

func TestLoadRejectsInvalidSessionTTL(t *testing.T) {
	t.Setenv("SESSION_TTL", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}
