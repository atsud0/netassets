package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	databaseURL := getenv("DATABASE_URL", "postgres://netassets:netassets@localhost:5432/netassets?sslmode=disable")
	migrationsDir := getenv("MIGRATIONS_DIR", "migrations")
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		fail("open database", err)
	}
	defer db.Close()

	if err := goose.SetDialect("postgres"); err != nil {
		fail("set migration dialect", err)
	}

	switch command {
	case "up", "down", "status", "version", "redo", "reset":
		if err := goose.Run(command, db, migrationsDir); err != nil {
			fail("run migration command", err)
		}
	default:
		fail("invalid migration command", fmt.Errorf("%q", command))
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func fail(message string, err error) {
	slog.Error(message, "error", err)
	os.Exit(1)
}
