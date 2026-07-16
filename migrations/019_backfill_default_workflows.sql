-- +goose Up
-- +goose StatementBegin

-- Backfill seeded default workflows for orgs that have none.
--
-- Migration 016 seeded defaults only for orgs existing at migration time, and
-- until now org provisioning via the admin CLI never seeded workflows — so
-- any org created through that path since 016 has no default workflows, and
-- AssignDefaultWorkflowToSpace fails for every space in it. This migration
-- repeats 016's seed block for exactly those orgs (and re-links their spaces
-- and items), making the seeded-defaults invariant hold for ALL orgs.
-- Application code now seeds on every org-creation path, so this cannot
-- recur; the migration itself is idempotent via the NOT EXISTS filter.

DO $$
DECLARE
    org_record RECORD;
    wf_ticket_id   UUID;
    wf_project_id  UUID;

    ts_open         UUID;
    ts_in_progress  UUID;
    ts_resolved     UUID;
    ts_closed       UUID;

    ps_backlog      UUID;
    ps_todo         UUID;
    ps_in_progress  UUID;
    ps_in_review    UUID;
    ps_done         UUID;
BEGIN
    FOR org_record IN
        SELECT id FROM organizations o
        WHERE o.deleted_at IS NULL
          AND NOT EXISTS (
              SELECT 1 FROM workflows w
              WHERE w.org_id = o.id AND w.is_default AND w.applies_to = 'tickets'
          )
    LOOP

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

        UPDATE spaces SET workflow_id = wf_ticket_id
        WHERE org_id = org_record.id AND type = 'service_desk' AND workflow_id IS NULL;

        UPDATE spaces SET workflow_id = wf_project_id
        WHERE org_id = org_record.id AND type = 'project' AND workflow_id IS NULL;

        UPDATE tickets t
        SET workflow_state_id = ws.id
        FROM spaces sp,
             workflow_states ws
        WHERE ws.workflow_id = wf_ticket_id
          AND ws.name = t.status
          AND sp.org_id = org_record.id
          AND t.space_id = sp.id
          AND t.workflow_state_id IS NULL;

        UPDATE tickets t
        SET workflow_state_id = ts_open
        FROM spaces sp
        WHERE sp.org_id = org_record.id
          AND t.space_id = sp.id
          AND t.workflow_state_id IS NULL;

        UPDATE project_items pi
        SET workflow_state_id = ws.id
        FROM spaces sp,
             workflow_states ws
        WHERE ws.workflow_id = wf_project_id
          AND ws.name = pi.status
          AND sp.org_id = org_record.id
          AND pi.space_id = sp.id
          AND pi.workflow_state_id IS NULL;

        UPDATE project_items pi
        SET workflow_state_id = ps_backlog
        FROM spaces sp
        WHERE sp.org_id = org_record.id
          AND pi.space_id = sp.id
          AND pi.workflow_state_id IS NULL;

    END LOOP;
END;
$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Data backfill — not reversible (removing workflows could orphan live
-- tickets/items pointing at their states). Down is a no-op by design.
SELECT 1;
-- +goose StatementEnd
