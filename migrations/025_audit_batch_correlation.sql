-- +goose Up
-- +goose StatementBegin

-- Audit batch correlation (P2.5, v0.3.2). Additive only: two nullable
-- columns on the append-only audit_log — no NOT NULL, no backfill, existing
-- rows keep NULL. batch_id groups the events of one atomic bulk grant
-- change so the viewer can render a batch as a single expandable row.
-- ticket_ref is a free-text operator-supplied reference stored WITHOUT a
-- foreign key: the audit log is self-contained, and deleting a referenced
-- ticket must never invalidate or orphan an audit row.
ALTER TABLE audit_log ADD COLUMN batch_id UUID;
ALTER TABLE audit_log ADD COLUMN ticket_ref TEXT;

CREATE INDEX idx_audit_log_batch_id ON audit_log (batch_id) WHERE batch_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX idx_audit_log_batch_id;
ALTER TABLE audit_log DROP COLUMN ticket_ref;
ALTER TABLE audit_log DROP COLUMN batch_id;

-- +goose StatementEnd
