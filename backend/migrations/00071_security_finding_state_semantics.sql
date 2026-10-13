-- +goose Up
-- Preserve the external status evidence instead of losing it during import.
-- The normalized value remains the legacy nine-value status vocabulary used by
-- the API; the raw value and reason explain any conservative conversion.
ALTER TABLE security_findings
    ADD COLUMN import_status_raw varchar(128),
    ADD COLUMN import_status_normalized varchar(24),
    ADD COLUMN import_status_mapping_reason text,
    ADD COLUMN duplicate_of_finding_id uuid REFERENCES security_findings(id) ON DELETE RESTRICT,
    ADD COLUMN risk_acceptance_expired_at timestamptz,
    ADD CONSTRAINT security_findings_duplicate_not_self_check
        CHECK (duplicate_of_finding_id IS NULL OR duplicate_of_finding_id <> id);

-- Candidates need the same evidence before they become formal findings. This
-- avoids making the mapping irrecoverable while an item is waiting for asset
-- association.
ALTER TABLE security_finding_import_candidates
    ADD COLUMN import_status_raw varchar(128),
    ADD COLUMN import_status_normalized varchar(24),
    ADD COLUMN import_status_mapping_reason text;

-- Existing rows predate evidence retention. Backfill the normalized value but
-- explicitly mark the reason so consumers never mistake it for a preserved raw
-- export value.
UPDATE security_findings
SET import_status_normalized = status,
    import_status_mapping_reason = CASE
        WHEN source = 'import' THEN '历史导入：迁移前未保存原始状态值'
        ELSE NULL
    END
WHERE source = 'import' AND import_status_normalized IS NULL;

CREATE INDEX security_findings_duplicate_of_idx
    ON security_findings(duplicate_of_finding_id)
    WHERE duplicate_of_finding_id IS NOT NULL;
CREATE INDEX security_findings_risk_acceptance_expired_idx
    ON security_findings(risk_acceptance_expired_at)
    WHERE risk_acceptance_expired_at IS NOT NULL;
CREATE INDEX security_finding_import_candidates_status_mapping_idx
    ON security_finding_import_candidates(import_status_normalized)
    WHERE import_status_normalized IS NOT NULL;

COMMENT ON COLUMN security_findings.import_status_raw IS
    '外部漏洞文件提供的原始状态值；仅导入来源使用，不能当作本地工作流状态直接执行。';
COMMENT ON COLUMN security_findings.import_status_normalized IS
    '外部状态映射后的本地九种漏洞状态值。';
COMMENT ON COLUMN security_findings.import_status_mapping_reason IS
    '原始状态映射到本地状态的原因，包含兼容和保守降级说明。';
COMMENT ON COLUMN security_findings.duplicate_of_finding_id IS
    '重复漏洞指向的主漏洞；duplicate 状态必须通过专用关联动作建立该关系。';
COMMENT ON COLUMN security_findings.risk_acceptance_expired_at IS
    '风险接受期限到期后由统一生命周期处理写入，保留到期证据而不新增漏洞状态值。';

-- +goose Down
DROP INDEX IF EXISTS security_finding_import_candidates_status_mapping_idx;
DROP INDEX IF EXISTS security_findings_risk_acceptance_expired_idx;
DROP INDEX IF EXISTS security_findings_duplicate_of_idx;
ALTER TABLE security_finding_import_candidates
    DROP COLUMN IF EXISTS import_status_mapping_reason,
    DROP COLUMN IF EXISTS import_status_normalized,
    DROP COLUMN IF EXISTS import_status_raw;
ALTER TABLE security_findings
    DROP CONSTRAINT IF EXISTS security_findings_duplicate_not_self_check,
    DROP COLUMN IF EXISTS risk_acceptance_expired_at,
    DROP COLUMN IF EXISTS duplicate_of_finding_id,
    DROP COLUMN IF EXISTS import_status_mapping_reason,
    DROP COLUMN IF EXISTS import_status_normalized,
    DROP COLUMN IF EXISTS import_status_raw;
