-- +goose Up
-- Revoking a grant is the one action a vendor cannot predict and cannot undo: the
-- task disappears from their list at the same instant. The self-targeted notice
-- excluded revoked grants entirely, so the only party that knows the collaboration
-- ended is the administrator who ended it - a party the vendor cannot see.
--
-- recorded_revoked_at is what makes that notice possible without spamming: a grant
-- that has been revoked for months must not keep reminding its holder every day, so
-- the notice is limited to revocations that happened recently.
ALTER TABLE remediation_grants
    ADD COLUMN IF NOT EXISTS recorded_revoked_at timestamptz;
CREATE INDEX IF NOT EXISTS remediation_grants_recently_revoked_idx
    ON remediation_grants (recorded_revoked_at DESC) WHERE recorded_revoked_at IS NOT NULL;

-- UpdateGrant is the single service path that ends a collaboration, so it is the
-- single place that stamps the moment. Row edits made outside the service keep a
-- null stamp and therefore produce no notice, which is the safe direction.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_touch_revoked_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'revoked' AND OLD.status IS DISTINCT FROM 'revoked' THEN
    NEW.recorded_revoked_at = now();
  ELSIF NEW.status <> 'revoked' THEN
    NEW.recorded_revoked_at = NULL;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS remediation_grants_revoked_at ON remediation_grants;
CREATE TRIGGER remediation_grants_revoked_at
    BEFORE UPDATE ON remediation_grants
    FOR EACH ROW EXECUTE FUNCTION remediation_touch_revoked_at();

-- +goose Down
DROP TRIGGER IF EXISTS remediation_grants_revoked_at ON remediation_grants;
DROP FUNCTION IF EXISTS remediation_touch_revoked_at();
DROP INDEX IF EXISTS remediation_grants_recently_revoked_idx;
ALTER TABLE remediation_grants DROP COLUMN IF EXISTS recorded_revoked_at;
