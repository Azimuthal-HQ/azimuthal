-- +goose Up
ALTER TABLE items ALTER COLUMN labels SET DEFAULT '{}';
UPDATE items SET labels = '{}' WHERE labels IS NULL;

-- +goose Down
ALTER TABLE items ALTER COLUMN labels DROP DEFAULT;
