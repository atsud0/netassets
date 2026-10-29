package securityworkorder_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/securityworkorder"
)

type importNotificationCounter struct {
	writes atomic.Int32
	cancel context.CancelFunc
}

func (c *importNotificationCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(strings.ToLower(data.SQL), "insert into notification_events") {
		c.writes.Add(1)
		if c.cancel != nil {
			c.cancel()
		}
	}
	return ctx
}
func (*importNotificationCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func notificationProfilePool(t *testing.T, f importFileFixture, c *importNotificationCounter) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(f.url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Tracer = c
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestFindingImportCompactsNotificationWrites(t *testing.T) {
	f := newImportFileFixture(t)
	counter := &importNotificationCounter{}
	svc := securityworkorder.NewService(notificationProfilePool(t, f, counter))
	result, err := svc.ImportFindings(context.Background(), f.actor, "", "digest.csv", strings.NewReader(f.csv(
		"first,high,一", " FIRST ,medium,重复不影响通知", "bad,invalid,失败不影响通知", "second,low,二",
	)), "compact-"+f.user, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.SuccessCount != 2 || result.DuplicateCount != 1 || result.FailureCount != 1 {
		t.Fatalf("unexpected row counts: %+v", result)
	}
	f.assertCounts(t, 2, 1, 3, 2)
	var count int
	var resourceID *string
	var label, level, severity string
	if err := f.pool.QueryRow(context.Background(), `SELECT item_count,resource_id,resource_label,level,details->>'severity' FROM notification_events WHERE actor_user_id=$1::uuid`, f.user).Scan(&count, &resourceID, &label, &level, &severity); err != nil {
		t.Fatal(err)
	}
	if count != 2 || resourceID != nil || label != "" || level != "info" || severity != "low" {
		t.Fatalf("digest count/resource/label/level/severity=%d/%v/%q/%q/%q", count, resourceID, label, level, severity)
	}
	if got := counter.writes.Load(); got != 1 {
		t.Fatalf("notification SQL writes=%d, want 1 per digest, not per imported row", got)
	}
	// Reusing a request digest adds exactly one new item, preserves aggregation,
	// and leaves no resource link to just the newest member.
	_, err = svc.ImportFindings(context.Background(), f.actor, "", "next.csv", strings.NewReader(f.csv("third,medium,三")), "compact-"+f.user, "")
	if err != nil {
		t.Fatal(err)
	}
	f.assertCounts(t, 3, 2, 5, 3)
	if err := f.pool.QueryRow(context.Background(), `SELECT item_count,resource_id,resource_label,level FROM notification_events WHERE actor_user_id=$1::uuid`, f.user).Scan(&count, &resourceID, &label, &level); err != nil {
		t.Fatal(err)
	}
	if count != 3 || resourceID != nil || label != "" || level != "warning" {
		t.Fatalf("existing digest count/resource/label/level=%d/%v/%q/%q", count, resourceID, label, level)
	}
}

func TestFindingImportNotificationFailureRollsBackWholeFile(t *testing.T) {
	f := newImportFileFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	counter := &importNotificationCounter{cancel: cancel}
	svc := securityworkorder.NewService(notificationProfilePool(t, f, counter))
	_, err := svc.ImportFindings(ctx, f.actor, "", "cancel-digest.csv", strings.NewReader(f.csv("first,high,一", "second,low,二")), "cancel-"+f.user, "")
	if err == nil {
		t.Fatal("notification cancellation must fail import")
	}
	f.assertCounts(t, 0, 0, 0, 0)
	if counter.writes.Load() != 1 {
		t.Fatal("did not reach notification flush")
	}
}

func TestFindingImportNotificationOwnersAndBlankBatches(t *testing.T) {
	for _, blank := range []bool{false, true} {
		t.Run(fmt.Sprintf("blank_batch_%t", blank), func(t *testing.T) {
			f := newImportFileFixture(t)
			ctx := context.Background()
			var ownerB string
			if err := f.pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','隔离责任人') RETURNING id::text`, "digest-owner-"+f.user).Scan(&ownerB); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, ownerB); err != nil {
					t.Error(err)
				}
			})
			if _, err := f.pool.Exec(ctx, `UPDATE asset_organizations SET owner_user_id=$1::uuid WHERE asset_id IN (SELECT id FROM assets WHERE created_by=$1::uuid)`, f.user); err != nil {
				t.Fatal(err)
			}
			valueB := "digest-other-" + f.value
			var assetB string
			if err := f.pool.QueryRow(ctx, `INSERT INTO assets(asset_type,asset_value,normalized_value,created_by) VALUES('domain',$1,$1,$2::uuid) RETURNING id::text`, valueB, f.user).Scan(&assetB); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(ctx, `INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary,owner_user_id) VALUES($1::uuid,$2::uuid,'owner',true,$3::uuid)`, assetB, f.org, ownerB); err != nil {
				t.Fatal(err)
			}
			payload := f.csv("first,high,一", "second,low,二") + "domain," + valueB + ",third,medium,三\n"
			counter := &importNotificationCounter{}
			requestID := "owners-" + f.user
			if blank {
				requestID = ""
			}
			result, err := securityworkorder.NewService(notificationProfilePool(t, f, counter)).ImportFindings(ctx, f.actor, "", "owners.csv", strings.NewReader(payload), requestID, "")
			if err != nil {
				t.Fatal(err)
			}
			if result.SuccessCount != 3 {
				t.Fatalf("success=%d", result.SuccessCount)
			}
			f.assertCounts(t, 3, 1, 4, 3)
			type digest struct {
				owner    string
				count    int
				resource *string
				label    string
			}
			rows, err := f.pool.Query(ctx, `SELECT target_user_id::text,item_count,resource_id,resource_label FROM notification_events WHERE actor_user_id=$1::uuid ORDER BY target_user_id,item_count`, f.user)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var digests []digest
			for rows.Next() {
				var d digest
				if err := rows.Scan(&d.owner, &d.count, &d.resource, &d.label); err != nil {
					t.Fatal(err)
				}
				digests = append(digests, d)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			expected := 2
			if blank {
				expected = 3
			}
			if len(digests) != expected || int(counter.writes.Load()) != expected {
				t.Fatalf("digests=%d writes=%d want %d", len(digests), counter.writes.Load(), expected)
			}
			ownerItems := map[string]int{}
			for _, d := range digests {
				ownerItems[d.owner] += d.count
				if d.count == 1 && (d.resource == nil || d.label == "") {
					t.Fatal("single-item targeted digest lost link")
				}
				if d.count > 1 && (d.resource != nil || d.label != "") {
					t.Fatal("aggregated targeted digest has misleading single-resource link")
				}
			}
			if ownerItems[f.user] != 2 || ownerItems[ownerB] != 1 {
				t.Fatalf("targeted digest counts leaked: %v", ownerItems)
			}
		})
	}
}
