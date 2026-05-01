-- +goose Up
-- +goose StatementBegin

-- ── Core workflow tables ──────────────────────────────────────────────────────

CREATE TABLE workflows (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    description TEXT,
    is_default  BOOLEAN     NOT NULL DEFAULT FALSE,
    applies_to  TEXT        NOT NULL CHECK (applies_to IN ('tickets','project_items','both')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

CREATE TABLE workflow_states (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id UUID        NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    category    TEXT        NOT NULL CHECK (category IN ('todo','in_progress','done')),
    color       TEXT        NOT NULL DEFAULT '#6b7280',
    position    INT         NOT NULL,
    is_initial  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workflow_id, name),
    UNIQUE (workflow_id, position)
);

-- Exactly one initial state per workflow.
CREATE UNIQUE INDEX idx_workflow_initial ON workflow_states (workflow_id) WHERE is_initial;

CREATE TABLE workflow_transitions (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id   UUID        NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    from_state_id UUID        NOT NULL REFERENCES workflow_states(id) ON DELETE CASCADE,
    to_state_id   UUID        NOT NULL REFERENCES workflow_states(id) ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workflow_id, from_state_id, to_state_id)
);

-- ── Add workflow columns to entity tables ─────────────────────────────────────

ALTER TABLE tickets       ADD COLUMN workflow_state_id UUID REFERENCES workflow_states(id);
ALTER TABLE project_items ADD COLUMN workflow_state_id UUID REFERENCES workflow_states(id);
ALTER TABLE spaces        ADD COLUMN workflow_id        UUID REFERENCES workflows(id) ON DELETE SET NULL;

-- ── Seed default workflows for all existing orgs ──────────────────────────────
-- Each org gets two default workflows: one for tickets, one for project_items.

DO $$
DECLARE
    org_record RECORD;
    wf_ticket_id   UUID;
    wf_project_id  UUID;

    -- ticket workflow state IDs
    ts_open         UUID;
    ts_in_progress  UUID;
    ts_resolved     UUID;
    ts_closed       UUID;

    -- project workflow state IDs
    ps_backlog      UUID;
    ps_todo         UUID;
    ps_in_progress  UUID;
    ps_in_review    UUID;
    ps_done         UUID;
BEGIN
    FOR org_record IN SELECT id FROM organizations WHERE deleted_at IS NULL LOOP

        -- ── Default Service Desk workflow ──────────────────────────────────
        INSERT INTO workflows (org_id, name, description, is_default, applies_to)
        VALUES (
            org_record.id,
            'Default Service Desk',
            'Default workflow for service desk tickets',
            TRUE,
            'tickets'
        )
        RETURNING id INTO wf_ticket_id;

        ts_open        := gen_random_uuid();
        ts_in_progress := gen_random_uuid();
        ts_resolved    := gen_random_uuid();
        ts_closed      := gen_random_uuid();

        INSERT INTO workflow_states (id, workflow_id, name, category, color, position, is_initial) VALUES
            (ts_open,        wf_ticket_id, 'open',        'todo',        '#3b82f6', 0, TRUE),
            (ts_in_progress, wf_ticket_id, 'in_progress', 'in_progress', '#f59e0b', 1, FALSE),
            (ts_resolved,    wf_ticket_id, 'resolved',    'done',        '#10b981', 2, FALSE),
            (ts_closed,      wf_ticket_id, 'closed',      'done',        '#6b7280', 3, FALSE);

        INSERT INTO workflow_transitions (workflow_id, from_state_id, to_state_id, name) VALUES
            (wf_ticket_id, ts_open,        ts_in_progress, 'Start Progress'),
            (wf_ticket_id, ts_open,        ts_closed,      'Close'),
            (wf_ticket_id, ts_in_progress, ts_resolved,    'Resolve'),
            (wf_ticket_id, ts_in_progress, ts_open,        'Reopen'),
            (wf_ticket_id, ts_in_progress, ts_closed,      'Close'),
            (wf_ticket_id, ts_resolved,    ts_closed,      'Close'),
            (wf_ticket_id, ts_resolved,    ts_open,        'Reopen'),
            (wf_ticket_id, ts_closed,      ts_open,        'Reopen');

        -- ── Default Project workflow ───────────────────────────────────────
        INSERT INTO workflows (org_id, name, description, is_default, applies_to)
        VALUES (
            org_record.id,
            'Default Project',
            'Default workflow for project items',
            TRUE,
            'project_items'
        )
        RETURNING id INTO wf_project_id;

        ps_backlog     := gen_random_uuid();
        ps_todo        := gen_random_uuid();
        ps_in_progress := gen_random_uuid();
        ps_in_review   := gen_random_uuid();
        ps_done        := gen_random_uuid();

        INSERT INTO workflow_states (id, workflow_id, name, category, color, position, is_initial) VALUES
            (ps_backlog,     wf_project_id, 'backlog',     'todo',        '#9ca3af', 0, TRUE),
            (ps_todo,        wf_project_id, 'todo',        'todo',        '#3b82f6', 1, FALSE),
            (ps_in_progress, wf_project_id, 'in_progress', 'in_progress', '#f59e0b', 2, FALSE),
            (ps_in_review,   wf_project_id, 'in_review',   'in_progress', '#8b5cf6', 3, FALSE),
            (ps_done,        wf_project_id, 'done',        'done',        '#10b981', 4, FALSE);

        INSERT INTO workflow_transitions (workflow_id, from_state_id, to_state_id, name) VALUES
            (wf_project_id, ps_backlog,     ps_todo,        'Start'),
            (wf_project_id, ps_backlog,     ps_in_progress, 'Start Progress'),
            (wf_project_id, ps_todo,        ps_in_progress, 'Start Progress'),
            (wf_project_id, ps_todo,        ps_backlog,     'Move to Backlog'),
            (wf_project_id, ps_in_progress, ps_in_review,   'Submit for Review'),
            (wf_project_id, ps_in_progress, ps_todo,        'Move to Todo'),
            (wf_project_id, ps_in_progress, ps_backlog,     'Move to Backlog'),
            (wf_project_id, ps_in_review,   ps_done,        'Approve'),
            (wf_project_id, ps_in_review,   ps_in_progress, 'Request Changes'),
            (wf_project_id, ps_done,        ps_in_progress, 'Reopen');

        -- Assign default workflows to existing spaces for this org.
        UPDATE spaces SET workflow_id = wf_ticket_id
        WHERE org_id = org_record.id AND type = 'service_desk' AND workflow_id IS NULL;

        UPDATE spaces SET workflow_id = wf_project_id
        WHERE org_id = org_record.id AND type = 'project' AND workflow_id IS NULL;

        -- Backfill workflow_state_id for existing tickets in this org's spaces.
        UPDATE tickets t
        SET workflow_state_id = ws.id
        FROM spaces sp,
             workflow_states ws
        WHERE ws.workflow_id = wf_ticket_id
          AND ws.name = t.status
          AND sp.org_id = org_record.id
          AND t.space_id = sp.id
          AND t.workflow_state_id IS NULL;

        -- Any tickets whose status doesn't match a state name fall back to initial state.
        UPDATE tickets t
        SET workflow_state_id = ts_open
        FROM spaces sp
        WHERE sp.org_id = org_record.id
          AND t.space_id = sp.id
          AND t.workflow_state_id IS NULL;

        -- Backfill workflow_state_id for existing project_items.
        UPDATE project_items pi
        SET workflow_state_id = ws.id
        FROM spaces sp,
             workflow_states ws
        WHERE ws.workflow_id = wf_project_id
          AND ws.name = pi.status
          AND sp.org_id = org_record.id
          AND pi.space_id = sp.id
          AND pi.workflow_state_id IS NULL;

        -- Any project_items whose status doesn't match fall back to backlog.
        UPDATE project_items pi
        SET workflow_state_id = ps_backlog
        FROM spaces sp
        WHERE sp.org_id = org_record.id
          AND pi.space_id = sp.id
          AND pi.workflow_state_id IS NULL;

    END LOOP;
END;
$$;

CREATE INDEX idx_workflows_org ON workflows (org_id);
CREATE INDEX idx_workflow_states_workflow ON workflow_states (workflow_id);
CREATE INDEX idx_workflow_transitions_workflow ON workflow_transitions (workflow_id);
CREATE INDEX idx_workflow_transitions_from ON workflow_transitions (from_state_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE spaces        DROP COLUMN IF EXISTS workflow_id;
ALTER TABLE project_items DROP COLUMN IF EXISTS workflow_state_id;
ALTER TABLE tickets       DROP COLUMN IF EXISTS workflow_state_id;
DROP TABLE IF EXISTS workflow_transitions;
DROP TABLE IF EXISTS workflow_states;
DROP TABLE IF EXISTS workflows;
-- +goose StatementEnd
