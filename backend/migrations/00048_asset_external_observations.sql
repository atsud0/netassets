-- +goose Up
CREATE TABLE asset_external_observations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    observation_type varchar(32) NOT NULL CHECK (observation_type IN ('domain_registration', 'tls_certificate')),
    endpoint_key varchar(512) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('pending', 'success', 'failed', 'unsupported')),
    source varchar(32),
    source_reference text,
    not_before timestamptz,
    expires_at timestamptz,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    last_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_success_at timestamptz,
    next_refresh_at timestamptz NOT NULL DEFAULT now(),
    consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    error_code varchar(64),
    error_message text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(asset_id, observation_type, endpoint_key)
);

CREATE INDEX asset_external_observations_asset_idx
    ON asset_external_observations(asset_id, observation_type, endpoint_key);
CREATE INDEX asset_external_observations_expiry_idx
    ON asset_external_observations(observation_type, expires_at)
    WHERE status = 'success' AND expires_at IS NOT NULL;
CREATE INDEX asset_external_observations_refresh_idx
    ON asset_external_observations(next_refresh_at)
    WHERE status <> 'unsupported';

COMMENT ON TABLE asset_external_observations IS '已登记网络资产的域名注册信息和 TLS 证书外部观测结果；不修改资产台账 updated_at';
COMMENT ON COLUMN asset_external_observations.endpoint_key IS '域名注册观测使用可注册根域，TLS 观测使用 host:port';
COMMENT ON COLUMN asset_external_observations.expires_at IS '外部对象到期时间，不等同于 assets.expires_at 资产生命周期有效期';

-- +goose Down
DROP TABLE IF EXISTS asset_external_observations;
