package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Environment               string
	Address                   string
	DatabaseURL               string
	StaticDir                 string
	DataDir                   string
	SessionCookieName         string
	SessionTTL                time.Duration
	SessionCookieSecure       bool
	BootstrapGroupCode        string
	BootstrapGroupName        string
	BootstrapGroupShortName   string
	BootstrapAdminUsername    string
	BootstrapAdminPassword    string
	BootstrapAdminDisplayName string
}

func Load() (Config, error) {
	sessionTTL, err := time.ParseDuration(getenv("SESSION_TTL", "12h"))
	if err != nil {
		return Config{}, fmt.Errorf("parse SESSION_TTL: %w", err)
	}

	sessionCookieSecure, err := strconv.ParseBool(getenv("SESSION_COOKIE_SECURE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("parse SESSION_COOKIE_SECURE: %w", err)
	}

	cfg := Config{
		Environment:               getenv("APP_ENV", "development"),
		Address:                   getenv("APP_ADDR", ":8080"),
		DatabaseURL:               getenv("DATABASE_URL", "postgres://netassets:netassets@localhost:5432/netassets?sslmode=disable"),
		StaticDir:                 getenv("STATIC_DIR", "../web/dist"),
		DataDir:                   getenv("DATA_DIR", "../data"),
		SessionCookieName:         getenv("SESSION_COOKIE_NAME", "netassets_session"),
		SessionTTL:                sessionTTL,
		SessionCookieSecure:       sessionCookieSecure,
		BootstrapGroupCode:        getenv("BOOTSTRAP_GROUP_CODE", "GROUP"),
		BootstrapGroupName:        getenv("BOOTSTRAP_GROUP_NAME", "示例集团"),
		BootstrapGroupShortName:   getenv("BOOTSTRAP_GROUP_SHORT_NAME", "集团总部"),
		BootstrapAdminUsername:    getenv("BOOTSTRAP_ADMIN_USERNAME", "admin"),
		BootstrapAdminPassword:    getenv("BOOTSTRAP_ADMIN_PASSWORD", ""),
		BootstrapAdminDisplayName: getenv("BOOTSTRAP_ADMIN_DISPLAY_NAME", "集团管理员"),
	}

	if cfg.Address == "" {
		return Config{}, fmt.Errorf("APP_ADDR must not be empty")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL must not be empty")
	}
	if cfg.DataDir == "" {
		return Config{}, fmt.Errorf("DATA_DIR must not be empty")
	}
	if cfg.SessionCookieName == "" {
		return Config{}, fmt.Errorf("SESSION_COOKIE_NAME must not be empty")
	}
	if cfg.BootstrapGroupCode == "" || cfg.BootstrapGroupName == "" || cfg.BootstrapAdminUsername == "" {
		return Config{}, fmt.Errorf("bootstrap group and administrator settings must not be empty")
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
