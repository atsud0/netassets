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

func TestLoadAutomaticDataQualityScanDefaults(t *testing.T) {
	t.Setenv("DATA_QUALITY_AUTO_SCAN_ENABLED", "true")
	t.Setenv("DATA_QUALITY_AUTO_SCAN_TIME", "02:30")
	t.Setenv("DATA_QUALITY_AUTO_SCAN_TIMEZONE", "Asia/Shanghai")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.DataQualityAutoScanEnabled || cfg.DataQualityAutoScanHour != 2 || cfg.DataQualityAutoScanMinute != 30 {
		t.Fatalf("automatic scan config = enabled:%v time:%02d:%02d", cfg.DataQualityAutoScanEnabled, cfg.DataQualityAutoScanHour, cfg.DataQualityAutoScanMinute)
	}
	if cfg.DataQualityAutoScanTimezone != "Asia/Shanghai" {
		t.Fatalf("DataQualityAutoScanTimezone = %q", cfg.DataQualityAutoScanTimezone)
	}
}

func TestLoadRejectsInvalidAutomaticDataQualityScanConfig(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		t.Setenv("DATA_QUALITY_AUTO_SCAN_ENABLED", "sometimes")
		if _, err := Load(); err == nil {
			t.Fatal("Load() error = nil, want invalid boolean error")
		}
	})
	t.Run("time", func(t *testing.T) {
		t.Setenv("DATA_QUALITY_AUTO_SCAN_TIME", "25:00")
		if _, err := Load(); err == nil {
			t.Fatal("Load() error = nil, want invalid time error")
		}
	})
	t.Run("timezone", func(t *testing.T) {
		t.Setenv("DATA_QUALITY_AUTO_SCAN_TIMEZONE", "Mars/Olympus")
		if _, err := Load(); err == nil {
			t.Fatal("Load() error = nil, want invalid timezone error")
		}
	})
}
