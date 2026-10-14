package assetcandidate

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/asset"
	"netassets/internal/auth"
)

// A discovery for the same asset value is ordinary duplicate work, and the
// pending uniqueness index (asset_candidates_pending_value_unique) is the only
// thing keeping two reviewers from creating two discoveries for one host. This
// test pins both halves of that contract: concurrent manual filings collapse to
// one pending discovery with a conflict for the loser, and concurrent
// confirmations of that discovery still produce exactly one working version.
func TestPendingDiscoveryIsSingularUnderConcurrentFilingsAndAccepts(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; integration test requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset asset candidate fixtures: %v", err)
	}

	var groupID, organizationID, userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('CANDIDATE_RACE_GROUP', '候选并发测试集团', 'group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid,'CANDIDATE_RACE_SUB','候选并发测试子公司','subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, display_name)
		VALUES ('candidate_race_operator','not-used','候选并发测试运营') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	principal := auth.Principal{UserID: userID, Username: "candidate_race_operator", DisplayName: "候选并发测试运营", Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{"asset.manage"}}
	service := NewService(pool, asset.NewService(pool))

	value := "race-" + strings.ReplaceAll(t.Name(), "/", "-") + ".example.com"
	input := CreateInput{AssetType: "domain", AssetValue: value, Name: "并发待确认资产", OrganizationID: organizationID, Source: "manual"}

	const filers = 4
	ids := make([]string, 0, filers)
	var mu sync.Mutex
	conflicts := 0
	var wg sync.WaitGroup
	for i := 0; i < filers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			candidate, err := service.Create(ctx, principal, input, "race-create", "127.0.0.1")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ids = append(ids, candidate.ID)
			case errors.Is(err, ErrConflict):
				conflicts++
			default:
				t.Errorf("concurrent create returned %v, want nil or ErrConflict", err)
			}
		}(i)
	}
	wg.Wait()
	if len(ids) != 1 || conflicts != filers-1 {
		t.Fatalf("concurrent filings produced %d discoveries and %d conflicts, want exactly 1 and %d; a duplicate must surface as a conflict, never as a server error", len(ids), conflicts, filers-1)
	}

	var pending int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM asset_candidates WHERE asset_type='domain' AND normalized_value=$1 AND status='pending'`, strings.ToLower(value)).Scan(&pending); err != nil {
		t.Fatalf("count pending discoveries: %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending discoveries for one value = %d, want 1", pending)
	}

	// Two reviewers confirming the same discovery at the same time must not both
	// win: one working version, one conflict.
	const accepters = 2
	var accepted int
	conflicts = 0
	wg = sync.WaitGroup{}
	for i := 0; i < accepters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := service.Accept(ctx, principal, ids[0], AcceptInput{}, "race-accept", "127.0.0.1")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				accepted++
			case errors.Is(err, ErrConflict):
				conflicts++
			default:
				t.Errorf("concurrent accept returned %v, want nil or ErrConflict", err)
			}
		}(i)
	}
	wg.Wait()
	if accepted != 1 || conflicts != accepters-1 {
		t.Fatalf("concurrent accepts succeeded %d times with %d conflicts, want 1 and %d", accepted, conflicts, accepters-1)
	}

	var drafts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inventory_asset_drafts d
		JOIN asset_candidates c ON c.working_version_draft_id = d.id
		WHERE c.id = $1::uuid`, ids[0]).Scan(&drafts); err != nil {
		t.Fatalf("count working versions: %v", err)
	}
	if drafts != 1 {
		t.Fatalf("working versions for one confirmed discovery = %d, want exactly 1", drafts)
	}
}
