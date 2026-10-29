package database

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Session defaults must not ALTER DATABASE/ROLE/SYSTEM or leak to other clients.
func TestOpenDefaultsJITForOLTPAndRespectsExplicitOverride(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("requires dedicated migrated test database")
	}
	ctx := context.Background()
	raw, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal("create baseline pool:", err)
	}
	defer raw.Close()
	var baseline string
	if err := raw.QueryRow(ctx, "SHOW jit").Scan(&baseline); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, jit, want string }{{"default", "", "off"}, {"explicit-on", "on", "on"}, {"explicit-off", "off", "off"}} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := url.Parse(databaseURL)
			if err != nil {
				t.Fatal("parse test database URL")
			}
			query := parsed.Query()
			query.Del("jit")
			if tc.jit != "" {
				query.Set("jit", tc.jit)
			}
			parsed.RawQuery = query.Encode()
			pool, err := Open(ctx, parsed.String())
			if err != nil {
				t.Fatal("open test pool:", err)
			}
			defer pool.Close()
			var got string
			if err := pool.QueryRow(ctx, "SHOW jit").Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("jit=%s, want %s", got, tc.want)
			}
			// Acquire simultaneously to validate more than the first pooled connection.
			first, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Release()
			second, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Release()
			for _, conn := range []*pgxpool.Conn{first, second} {
				if err := conn.QueryRow(ctx, "SHOW jit").Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != tc.want {
					t.Errorf("pooled connection jit=%s, want %s", got, tc.want)
				}
				var count int
				var values string
				if err := conn.QueryRow(ctx, `SELECT count(*)::int,jsonb_agg(n ORDER BY n)::text FROM generate_series(1,8) n`).Scan(&count, &values); err != nil {
					t.Fatal(err)
				}
				if count != 8 || values != "[1, 2, 3, 4, 5, 6, 7, 8]" {
					t.Fatalf("query semantics changed: %d %s", count, values)
				}
			}
		})
	}
	var after string
	if err := raw.QueryRow(ctx, "SHOW jit").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != baseline {
		t.Errorf("unrelated connection default changed from %s to %s", baseline, after)
	}
}
