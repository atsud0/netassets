package savedfilter_test

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func mustCreateUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	username := "sf_user_" + mustRandomSuffix(t)
	var id string
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO users (username, password_hash, display_name) VALUES ($1,'not-used',$1) RETURNING id::text`,
		username).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// mustRandomSuffix keeps concurrent runs of the same suite from colliding on the
// unique (user_id, page_key, name) and username indexes.
func mustRandomSuffix(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("read random bytes: %v", err)
	}
	return strings.ToLower(hex.EncodeToString(buf))
}
