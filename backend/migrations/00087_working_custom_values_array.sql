-- +goose Up
-- Working custom values are always arrays because diff and snapshot queries
-- consume them with jsonb_array_elements.
UPDATE inventory_asset_drafts SET custom_values='[]'::jsonb
WHERE jsonb_typeof(custom_values) IS DISTINCT FROM 'array';
UPDATE inventory_business_system_drafts SET custom_values='[]'::jsonb
WHERE jsonb_typeof(custom_values) IS DISTINCT FROM 'array';
ALTER TABLE inventory_asset_drafts
  ADD CONSTRAINT inventory_asset_drafts_custom_values_array CHECK (custom_values IS NOT NULL AND jsonb_typeof(custom_values)='array');
ALTER TABLE inventory_business_system_drafts
  ADD CONSTRAINT inventory_business_system_drafts_custom_values_array CHECK (custom_values IS NOT NULL AND jsonb_typeof(custom_values)='array');

-- +goose Down
ALTER TABLE inventory_asset_drafts DROP CONSTRAINT IF EXISTS inventory_asset_drafts_custom_values_array;
ALTER TABLE inventory_business_system_drafts DROP CONSTRAINT IF EXISTS inventory_business_system_drafts_custom_values_array;
