-- +goose Up
-- Older working versions could explicitly persist the JSON scalar null when a
-- form had no custom fields. Snapshot and diff queries consume this column as
-- an array, so normalize historical rows before those queries see them.
UPDATE inventory_asset_drafts
SET custom_values = '[]'::jsonb
WHERE jsonb_typeof(custom_values) IS DISTINCT FROM 'array';

UPDATE inventory_business_system_drafts
SET custom_values = '[]'::jsonb
WHERE jsonb_typeof(custom_values) IS DISTINCT FROM 'array';

-- +goose Down
-- The migration only repairs malformed historical values; there is no safe
-- inverse because the original scalar null carries no information.
