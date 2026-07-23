-- +goose Up
-- +goose StatementBegin

-- Backfill the one-step-back reverse transitions on default service-desk
-- ticket workflows for pre-existing installs.
--
-- Migrations 016/019 seeded the default ticket workflow WITHOUT the reverse
-- edges resolved->in_progress, closed->resolved, closed->in_progress, so a
-- resolved ticket could only reach in_progress by first going through `open`.
-- The application seed (internal/db/adapters/workflows.go seedTicketWorkflow)
-- and the hardcoded Go state machine (internal/core/tickets/status.go) now
-- include these edges for new orgs; this migration adds the identical set to
-- workflows that already exist.
--
-- Scoped to is_default + name='Default Service Desk' so admin-customised
-- workflows that deliberately omit an edge are left untouched. State ids are
-- per-workflow random UUIDs, so states are joined by name. Idempotent via the
-- UNIQUE(workflow_id, from_state_id, to_state_id) constraint from migration
-- 016 — safe to re-run and safe against orgs that already have the edge.

INSERT INTO workflow_transitions (workflow_id, from_state_id, to_state_id, name)
SELECT w.id, s_from.id, s_to.id, edge.name
FROM workflows w
JOIN LATERAL (
    VALUES
        ('resolved', 'in_progress', 'Resume Progress'),
        ('closed',   'resolved',    'Reopen'),
        ('closed',   'in_progress', 'Resume Progress')
) AS edge(from_name, to_name, name) ON TRUE
JOIN workflow_states s_from ON s_from.workflow_id = w.id AND s_from.name = edge.from_name
JOIN workflow_states s_to   ON s_to.workflow_id   = w.id AND s_to.name   = edge.to_name
WHERE w.applies_to = 'tickets'
  AND w.is_default = TRUE
  AND w.name = 'Default Service Desk'
ON CONFLICT (workflow_id, from_state_id, to_state_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- Remove exactly the reverse edges this migration inserts, scoped the same way.
DELETE FROM workflow_transitions t
USING workflows w, workflow_states f, workflow_states s
WHERE t.workflow_id = w.id
  AND w.applies_to = 'tickets'
  AND w.is_default = TRUE
  AND w.name = 'Default Service Desk'
  AND t.from_state_id = f.id
  AND t.to_state_id = s.id
  AND (
        (f.name = 'resolved' AND s.name = 'in_progress')
     OR (f.name = 'closed'   AND s.name = 'resolved')
     OR (f.name = 'closed'   AND s.name = 'in_progress')
  );

-- +goose StatementEnd
