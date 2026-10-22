package securityworkorder

import (
	"context"
	"errors"
	"testing"

	"netassets/internal/customfield"
)

// Exercise the public custom-field service against real formal resources and
// stored required values; an optional-only payload must preserve the latter.
func TestFormalCustomFieldPartialSavePreservesRequiredValues(t *testing.T) {
	pool, p, org, findingID := reviewFixture(t)
	ctx := context.Background()
	finding, err := NewService(pool, t.TempDir()).GetFinding(ctx, p, findingID)
	if err != nil {
		t.Fatal(err)
	}
	var assetID string
	if err = pool.QueryRow(ctx, `INSERT INTO assets(asset_type,asset_value,normalized_value,asset_name,created_by,updated_by) VALUES('ip','192.0.2.89','192.0.2.89','Custom fields asset',$1::uuid,$1::uuid) RETURNING id::text`, p.UserID).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary) VALUES($1::uuid,$2::uuid,'owner',true)`, assetID, org); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ typ, id, table string }{{"asset", assetID, "assets"}, {"business_system", finding.BusinessSystemID, "business_systems"}} {
		t.Run(tc.typ, func(t *testing.T) {
			// Definitions are global, unlike the organization-scoped fixture data.
			// Clean up only this test's definitions, never unrelated configuration.
			t.Cleanup(func() {
				_, _ = pool.Exec(context.Background(), `DELETE FROM custom_field_definitions WHERE resource_type=$1 AND field_key IN ('partial_required','partial_optional')`, tc.typ)
			})
			var requiredID string
			if err := pool.QueryRow(ctx, `INSERT INTO custom_field_definitions(resource_type,field_key,field_name,field_type,is_required,is_enabled,options) VALUES($1,'partial_required','Required','text',true,true,'[]') RETURNING id::text`, tc.typ).Scan(&requiredID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO custom_field_definitions(resource_type,field_key,field_name,field_type,is_required,is_enabled,options) VALUES($1,'partial_optional','Optional','text',false,true,'[]')`, tc.typ); err != nil {
				t.Fatal(err)
			}
			service := customfield.NewService(pool)
			version := 1
			save := func(values ...customfield.Value) error {
				return service.SaveValues(ctx, p, tc.typ, tc.id, customfield.ValuesInput{ExpectedVersion: &version, Values: values}, "partial-fields", "127.0.0.1")
			}
			optional := customfield.Value{FieldKey: "partial_optional", Value: "new optional value"}
			assertStored := func(wantVersion int, wantRequired string, wantOptional string) {
				t.Helper()
				var actual int
				if err := pool.QueryRow(ctx, `SELECT version FROM `+tc.table+` WHERE id=$1::uuid`, tc.id).Scan(&actual); err != nil {
					t.Fatal(err)
				}
				if actual != wantVersion {
					t.Fatalf("resource version=%d, want %d", actual, wantVersion)
				}
				for key, want := range map[string]string{"partial_required": wantRequired, "partial_optional": wantOptional} {
					var got string
					if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT v.value #>> '{}' FROM custom_field_values v JOIN custom_field_definitions d ON d.id=v.field_definition_id WHERE v.resource_type=$1 AND v.resource_id=$2::uuid AND d.field_key=$3),'')`, tc.typ, tc.id, key).Scan(&got); err != nil {
						t.Fatal(err)
					}
					if got != want {
						t.Fatalf("%s=%q, want %q", key, got, want)
					}
				}
			}
			if err := save(optional); !errors.Is(err, customfield.ErrInvalid) {
				t.Fatalf("missing required value=%v", err)
			}
			assertStored(1, "", "")
			if _, err := pool.Exec(ctx, `INSERT INTO custom_field_values(resource_type,resource_id,field_definition_id,value,updated_by) VALUES($1,$2::uuid,$3::uuid,'"stored required value"',$4::uuid)`, tc.typ, tc.id, requiredID, p.UserID); err != nil {
				t.Fatal(err)
			}
			if err := save(optional); err != nil {
				t.Fatalf("partial save with stored required value: %v", err)
			}
			assertStored(2, "stored required value", "new optional value")
			optional.Value = "stale update"
			if err := save(optional); !errors.Is(err, customfield.ErrVersionConflict) {
				t.Fatalf("stale partial save=%v", err)
			}
			assertStored(2, "stored required value", "new optional value")
			version = 2
			for _, blank := range []any{"", "  ", nil} {
				if err := save(optional, customfield.Value{FieldKey: "partial_required", Value: blank}); !errors.Is(err, customfield.ErrInvalid) {
					t.Fatalf("explicit blank %#v=%v", blank, err)
				}
				assertStored(2, "stored required value", "new optional value")
			}
		})
	}
}
