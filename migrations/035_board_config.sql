-- +goose Up
-- +goose StatementBegin

-- Per-space board configuration: named, orderable columns, each mapping one or
-- more item statuses, with an optional soft WIP limit.
--
-- Relational rather than a JSONB blob on spaces, for one reason that matters
-- more than the others: the "every status is mapped, and to exactly one
-- column" invariant becomes a database constraint instead of application code
-- that can be forgotten. board_column_statuses' primary key is (space_id,
-- status), so a status cannot land in two columns; the FK to board_columns is
-- ON DELETE RESTRICT, so a column cannot be dropped while it still owns
-- statuses. Together they make an unmapped or double-mapped status impossible
-- at the storage layer. A JSONB document would need a trigger or a CHECK with
-- a subquery to say the same thing, and neither survives a careless writer.
-- Ordering and name uniqueness fall out of ordinary constraints too, and the
-- shape mirrors workflow_states, which is the closest existing thing.
--
-- NOTE: this migration deliberately backfills NOTHING. A space with no rows
-- here has no custom board, and the API derives the default configuration from
-- the space's workflow states exactly as the board already did — so every
-- existing space renders byte-identically until someone customises it. Seeding
-- rows for every space would mean reproducing that derivation twice and hoping
-- the two agree; absence-means-default cannot drift.

CREATE TABLE board_columns (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id   UUID        NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name       TEXT        NOT NULL CHECK (name <> ''),
    position   INT         NOT NULL,
    -- NULL means no limit. A limit of zero would mean "no work may be in this
    -- column", which is not a WIP limit, it is a closed column.
    wip_limit  INT         CHECK (wip_limit IS NULL OR wip_limit > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (space_id, name),
    -- Deferred so a reorder can renumber columns inside one transaction
    -- without shuffling through temporary positions to dodge the constraint.
    CONSTRAINT board_columns_space_position_key UNIQUE (space_id, position)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX idx_board_columns_space ON board_columns (space_id, position);

CREATE TABLE board_column_statuses (
    space_id  UUID NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    status    TEXT NOT NULL CHECK (status <> ''),
    -- RESTRICT, not CASCADE: dropping a column must re-home its statuses
    -- first. Under CASCADE the mappings would vanish silently and their items
    -- would fall off the board with nothing to show for it.
    column_id UUID NOT NULL REFERENCES board_columns(id) ON DELETE RESTRICT,
    PRIMARY KEY (space_id, status)
);

CREATE INDEX idx_board_column_statuses_column ON board_column_statuses (column_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS board_column_statuses;
DROP TABLE IF EXISTS board_columns;
-- +goose StatementEnd
