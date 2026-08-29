-- +goose Up
ALTER TABLE data_quality_rules
    DROP CONSTRAINT data_quality_rules_threshold_check;

ALTER TABLE data_quality_rules
    ADD CONSTRAINT data_quality_rules_threshold_check
        CHECK (
            (rule_type = 'required' AND stale_after_days IS NULL AND expiry_warning_days IS NULL)
            OR
            (rule_type = 'stale' AND target_field = 'updated_at' AND stale_after_days BETWEEN 1 AND 3650 AND expiry_warning_days IS NULL)
            OR
            (rule_type = 'expiry' AND resource_type = 'asset'
                AND target_field IN ('expires_at', 'domain_registration_expires_at', 'tls_certificate_expires_at')
                AND stale_after_days IS NULL AND expiry_warning_days BETWEEN 0 AND 3650)
        );

INSERT INTO data_quality_rules(
    rule_name,description,resource_type,rule_type,target_field,severity,expiry_warning_days,conditions,is_enabled,sort_order
) VALUES
    (
        '域名注册到期预警',
        '通过 RDAP 获取域名注册到期时间，并在到期前 30 天生成质量整改问题。',
        'asset','expiry','domain_registration_expires_at','warning',30,
        '[{"field":"asset_type","operator":"in","values":["domain","url"]}]'::jsonb,
        true,900
    ),
    (
        'TLS 证书到期预警',
        '通过公网 TLS 握手获取证书到期时间，并在到期前 30 天生成质量整改问题。',
        'asset','expiry','tls_certificate_expires_at','warning',30,
        '[{"field":"asset_type","operator":"in","values":["domain","url"]},{"field":"network_scope","operator":"eq","values":["public"]}]'::jsonb,
        true,910
    )
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM data_quality_rules
WHERE rule_type='expiry'
  AND target_field IN ('domain_registration_expires_at','tls_certificate_expires_at');

ALTER TABLE data_quality_rules
    DROP CONSTRAINT data_quality_rules_threshold_check;

ALTER TABLE data_quality_rules
    ADD CONSTRAINT data_quality_rules_threshold_check
        CHECK (
            (rule_type = 'required' AND stale_after_days IS NULL AND expiry_warning_days IS NULL)
            OR
            (rule_type = 'stale' AND target_field = 'updated_at' AND stale_after_days BETWEEN 1 AND 3650 AND expiry_warning_days IS NULL)
            OR
            (rule_type = 'expiry' AND resource_type = 'asset' AND target_field = 'expires_at'
                AND stale_after_days IS NULL AND expiry_warning_days BETWEEN 0 AND 3650)
        );
