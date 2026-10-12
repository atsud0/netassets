-- +goose Up
-- A reviewer must be independent from the person who submitted the remediation
-- material. The service checks this before the insert; this trigger protects
-- direct SQL and future write paths as well.
-- +goose StatementBegin
CREATE FUNCTION remediation_prevent_self_review() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE submission_author uuid;
BEGIN
    SELECT author_id INTO submission_author
    FROM remediation_submissions
    WHERE id = NEW.submission_id;
    IF submission_author IS NOT NULL AND submission_author = NEW.reviewer_id THEN
        RAISE EXCEPTION 'remediation submitter cannot review own submission'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER remediation_prevent_self_review
BEFORE INSERT OR UPDATE OF submission_id, reviewer_id ON remediation_reviews
FOR EACH ROW EXECUTE FUNCTION remediation_prevent_self_review();

-- +goose Down
DROP TRIGGER remediation_prevent_self_review ON remediation_reviews;
DROP FUNCTION remediation_prevent_self_review();
