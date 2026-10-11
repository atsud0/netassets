-- +goose Up
ALTER TABLE security_finding_imports
    DROP CONSTRAINT IF EXISTS security_finding_imports_file_type_check;
ALTER TABLE security_finding_imports
    ADD CONSTRAINT security_finding_imports_file_type_check CHECK (file_type IN ('csv', 'xlsx', 'json'));

-- +goose Down
ALTER TABLE security_finding_imports
    DROP CONSTRAINT IF EXISTS security_finding_imports_file_type_check;
ALTER TABLE security_finding_imports
    ADD CONSTRAINT security_finding_imports_file_type_check CHECK (file_type IN ('csv', 'xlsx'));
