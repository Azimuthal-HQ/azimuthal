-- +goose Up
-- +goose StatementBegin

-- Enforce at the database level what projects.SprintService.StartSprint has
-- intended since it was written: at most one active sprint per space. The
-- service reads GetActiveBySpace and refuses to start a second active sprint,
-- but that read-then-write has a TOCTOU window under concurrency — two starts
-- interleaving could both pass the check and leave a space with two active
-- sprints, which the board and roadmap scoping assume cannot happen. A partial
-- unique index closes the window: the second UPDATE ... SET status = 'active'
-- fails on the constraint, and the adapter maps that violation back to
-- ErrSprintActive so the API still returns 409 rather than a 500.
--
-- Partial (WHERE status = 'active') so any number of planned and completed
-- sprints may coexist per space — only the active one is unique.
CREATE UNIQUE INDEX idx_sprints_one_active_per_space
    ON sprints (space_id)
    WHERE status = 'active';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_sprints_one_active_per_space;
-- +goose StatementEnd
