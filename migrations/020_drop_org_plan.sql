-- +goose Up
-- +goose StatementBegin

-- Azimuthal is fully featured for everyone: no plans, no tiers, no
-- community/enterprise split — ever. The plan column was tier language
-- left over from early scaffolding and is used for nothing.
ALTER TABLE organizations DROP COLUMN IF EXISTS plan;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE organizations ADD COLUMN plan TEXT NOT NULL DEFAULT 'community';
-- +goose StatementEnd
