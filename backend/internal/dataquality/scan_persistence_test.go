package dataquality

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type scanPersistenceTx struct {
	pgx.Tx
	chunks []int
	failAt int
}

func (tx *scanPersistenceTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	resourceIDs := args[2].([]string)
	for _, argument := range args[3:7] {
		if len(argument.([]string)) != len(resourceIDs) {
			return pgconn.CommandTag{}, errors.New("mismatched scan columns")
		}
	}
	tx.chunks = append(tx.chunks, len(resourceIDs))
	if tx.failAt == len(tx.chunks) {
		return pgconn.CommandTag{}, errors.New("scan persistence failure")
	}
	return pgconn.NewCommandTag(fmt.Sprintf("INSERT 0 %d", len(resourceIDs))), nil
}

func TestScanPersistenceBoundsRoundTripsWithoutDroppingIssues(t *testing.T) {
	for _, count := range []int{0, 1, 500, 501, 1001, 24001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			tx := &scanPersistenceTx{}
			if err := persistScanIssues(context.Background(), tx, Rule{}, make([]ScanIssue, count), "", nil); err != nil {
				t.Fatal(err)
			}
			if len(tx.chunks) != (count+499)/500 {
				t.Fatalf("round trips=%d, want %d", len(tx.chunks), (count+499)/500)
			}
			var total int
			for _, size := range tx.chunks {
				if size < 1 || size > 500 {
					t.Fatalf("unbounded chunk: %d", size)
				}
				total += size
			}
			if total != count {
				t.Fatalf("persisted %d issues, want %d", total, count)
			}
		})
	}
}

func TestScanPersistenceStopsOnFailedChunk(t *testing.T) {
	tx := &scanPersistenceTx{failAt: 2}
	if err := persistScanIssues(context.Background(), tx, Rule{}, make([]ScanIssue, 1001), "", nil); err == nil {
		t.Fatal("persistence failure must propagate")
	}
	if len(tx.chunks) != 2 {
		t.Fatalf("attempted %d chunks after failure, want 2", len(tx.chunks))
	}
}
