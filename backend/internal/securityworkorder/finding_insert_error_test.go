package securityworkorder

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestFindingInsertErrorOnlyClassifiesFindingIdentityConflict(t *testing.T) {
	for _, tc := range []struct {
		name       string
		code       string
		constraint string
		duplicate  bool
	}{
		{"finding identity race", "23505", "security_findings_batch_asset_title_unique", true},
		{"finding code collision", "23505", "security_findings_finding_code_key", false},
		{"other unique violation", "23505", "security_findings_pkey", false},
		{"foreign key violation", "23503", "security_findings_asset_id_fkey", false},
		{"wrong SQLSTATE", "23503", "security_findings_batch_asset_title_unique", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := &pgconn.PgError{Code: tc.code, ConstraintName: tc.constraint}
			err := findingInsertError(fmt.Errorf("wrapped database error: %w", original))
			if errors.Is(err, ErrConflict) != tc.duplicate {
				t.Fatalf("conflict = %v, want %v: %v", errors.Is(err, ErrConflict), tc.duplicate, err)
			}
			var got *pgconn.PgError
			if !errors.As(err, &got) || got != original {
				t.Fatalf("original PostgreSQL error not retained: %v", err)
			}
		})
	}
	original := errors.New("connection closed")
	err := findingInsertError(original)
	if errors.Is(err, ErrConflict) || !errors.Is(err, original) {
		t.Fatalf("non-PostgreSQL error changed classification: %v", err)
	}
}
