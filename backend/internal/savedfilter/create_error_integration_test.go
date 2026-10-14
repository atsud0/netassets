package savedfilter_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/savedfilter"
)

// NormalizeInput is a pure function, so its tests alone cannot catch a caller that
// throws the specific error away on the way to the API. That is exactly what Create and
// Update used to do: they reported a generic ErrInvalid for every refusal, which is
// what left users staring at "请检查页面、名称和筛选条件" with no idea which field to
// change. These tests drive the real service against a migrated database.

func openSavedFilterDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; requires a dedicated migrated PostgreSQL database")
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// A single over-long search keyword must reach the caller as a named field error, so
// the HTTP layer can tell the user which input to shorten.
func TestCreateReturnsTheOffendingFilterField(t *testing.T) {
	pool := openSavedFilterDB(t)
	service := savedfilter.NewService(pool)
	principal := auth.Principal{UserID: mustCreateUser(t, pool)}

	_, err := service.Create(t.Context(), principal, savedfilter.Input{
		PageKey: "assets", Name: "超长关键词视图",
		Filters: map[string]string{"q": strings.Repeat("a", 201)},
	}, "request-create", "127.0.0.1")

	var fieldErr *savedfilter.FieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("Create() error = %v, want *savedfilter.FieldError so the API can name the field", err)
	}
	if fieldErr.Field != "q" {
		t.Fatalf("error field = %q, want q", fieldErr.Field)
	}
	if !strings.Contains(fieldErr.Reason, "搜索关键词") || !strings.Contains(fieldErr.Reason, "200") {
		t.Fatalf("error reason %q does not tell the user which filter and which limit", fieldErr.Reason)
	}
}

// Update must keep the reason too: renaming or re-defaulting a view is the other way
// the same over-long filter comes back.
func TestUpdateReturnsTheOffendingFilterField(t *testing.T) {
	pool := openSavedFilterDB(t)
	service := savedfilter.NewService(pool)
	principal := auth.Principal{UserID: mustCreateUser(t, pool)}

	created, err := service.Create(t.Context(), principal, savedfilter.Input{
		PageKey: "assets", Name: "更新用例视图", Filters: map[string]string{"status": "active"},
	}, "request-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = service.Update(t.Context(), principal, created.ID, savedfilter.Input{
		PageKey: "assets", Name: created.Name,
		Filters: map[string]string{"type": "host"},
	}, "request-update", "127.0.0.1")
	var fieldErr *savedfilter.FieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("Update() error = %v, want *savedfilter.FieldError", err)
	}
	if fieldErr.Field != "type" {
		t.Fatalf("error field = %q, want type", fieldErr.Field)
	}
}

// A refusal must not create a row: a view saved from a rejected filter would come back
// on the next page load and then fail again to apply.
func TestRejectedFilterStoresNothing(t *testing.T) {
	pool := openSavedFilterDB(t)
	service := savedfilter.NewService(pool)
	principal := auth.Principal{UserID: mustCreateUser(t, pool)}

	name := "不应落库-" + mustRandomSuffix(t)
	if _, err := service.Create(t.Context(), principal, savedfilter.Input{
		PageKey: "assets", Name: name,
		Filters: map[string]string{"q": strings.Repeat("b", 300)},
	}, "request-create", "127.0.0.1"); err == nil {
		t.Fatal("Create() accepted an over-long keyword")
	}

	var stored int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM saved_filter_views WHERE user_id=$1::uuid AND name=$2`,
		principal.UserID, name).Scan(&stored); err != nil {
		t.Fatalf("count saved views: %v", err)
	}
	if stored != 0 {
		t.Fatalf("a rejected filter stored %d rows", stored)
	}
}
