import { useState } from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { QueryFilterBuilder } from '../QueryFilterBuilder';
import {
  ASSIGNEE_ME,
  vectorOnlyFieldsReason,
  type QueryDoc,
} from '../../../lib/views/query';

// P4: the builder's one rule that is not a nicety.
//
// `kinds` and `sprint_ids` read columns that exist on project_items and do not
// exist on tickets, so naming either alongside Beacon is a 422 on the WHOLE
// document — not an empty Beacon half. The builder therefore has to refuse the
// combination before a round trip, and it has to drop a type selection at the
// moment the module toggle causes it, where the person can see it happen.
//
// Both directions are asserted below: with Vector alone the controls are live
// and a selection reaches the document; the moment Beacon joins, the reason is
// shown, the controls go inert, and the selection is gone from the emitted
// document. Delete the prune, or the `disabled`, or the hint, and one of these
// fails.

vi.mock('../../../lib/api', () => ({
  useSpaces: () => ({ data: [{ id: 's1', name: 'Platform', slug: 'plat', key: 'PLAT', type: 'vector' }] }),
  useItemTypes: () => ({
    data: [
      { id: 't1', slug: 'bug', name: 'Bug', archived_at: null },
      { id: 't2', slug: 'story', name: 'Story', archived_at: null },
    ],
  }),
  useSprints: () => ({ data: [] }),
  // Reached through PersonTeamPicker, which the assignee control reuses.
  useMemberSearch: () => ({ data: [], isLoading: false }),
  useTeams: () => ({ data: [] }),
}));

const vectorOnly: QueryDoc = {
  v: 1,
  filter: { modules: ['vector'] },
  sort: { field: 'updated_at', dir: 'desc' },
};

/** Drives the controlled builder and records every document it emits. */
function renderBuilder(initial: QueryDoc = vectorOnly) {
  const emitted: QueryDoc[] = [];
  function Harness() {
    const [doc, setDoc] = useState(initial);
    return (
      <QueryFilterBuilder
        orgId="org-1"
        value={doc}
        onChange={(next) => {
          emitted.push(next);
          setDoc(next);
        }}
      />
    );
  }
  render(<Harness />);
  return { emitted, last: () => emitted[emitted.length - 1] };
}

describe('QueryFilterBuilder — the vector-only fields', () => {
  it('offers types and sprints when Vector is the only module', () => {
    renderBuilder();

    expect(screen.getByRole('button', { name: 'Bug' })).toBeEnabled();
    expect(screen.queryByTestId('view-kinds-reason')).toBeNull();
    expect(screen.queryByTestId('view-sprints-reason')).toBeNull();
  });

  it('records a type selection on the document while Vector is alone', () => {
    const { last } = renderBuilder();

    fireEvent.click(screen.getByRole('button', { name: 'Bug' }));

    expect(last().filter.kinds).toEqual(['bug']);
  });

  it('disables both controls and explains why the moment Beacon joins', () => {
    const { last } = renderBuilder();

    fireEvent.click(screen.getByRole('button', { name: 'Bug' }));
    fireEvent.click(screen.getByRole('button', { name: 'Beacon' }));

    // The explanation is the vocabulary's own sentence, not a second copy of
    // the rule written here.
    const reason = vectorOnlyFieldsReason(['beacon', 'vector'])!;
    expect(screen.getByTestId('view-kinds-reason')).toHaveTextContent(reason);
    expect(screen.getByTestId('view-sprints-reason')).toHaveTextContent(reason);
    expect(screen.getByRole('button', { name: 'Bug' })).toBeDisabled();

    // And the selection is GONE from the document — not merely hidden. A
    // hidden-but-retained `kinds` would 422 the save with an explanation the
    // user could no longer see.
    const doc = last();
    expect(doc.filter.modules).toEqual(['beacon', 'vector']);
    expect(doc.filter.kinds).toBeUndefined();
  });

  it('restores the controls when Beacon is removed again', () => {
    renderBuilder();

    fireEvent.click(screen.getByRole('button', { name: 'Beacon' }));
    expect(screen.getByTestId('view-kinds-reason')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Beacon' }));
    expect(screen.queryByTestId('view-kinds-reason')).toBeNull();
    expect(screen.getByRole('button', { name: 'Bug' })).toBeEnabled();
  });

  it('explains the rule while no module is chosen at all', () => {
    const { last } = renderBuilder();

    fireEvent.click(screen.getByRole('button', { name: 'Vector' }));

    expect(last().filter.modules).toEqual([]);
    expect(screen.getByTestId('view-kinds-reason')).toHaveTextContent(
      vectorOnlyFieldsReason([])!,
    );
  });
});

describe('QueryFilterBuilder — the viewer-relative assignee', () => {
  it('stores the literal "me" token rather than the reader’s id', () => {
    const { last } = renderBuilder();

    fireEvent.click(screen.getByRole('button', { name: 'Me' }));

    // Substituting a uuid here would freeze a shared view to one person and
    // silently change what everybody else sees.
    expect(last().filter.assignees).toEqual([ASSIGNEE_ME]);
  });

  it('offers Unassigned as a first-class choice beside it', () => {
    const { last } = renderBuilder();

    fireEvent.click(screen.getByRole('button', { name: 'Me' }));
    fireEvent.click(screen.getByRole('button', { name: 'Unassigned' }));

    expect(last().filter.assignees).toEqual(['me', 'unassigned']);
  });
});

// v2: date ranges and per-field exclusion.
//
// The two things worth guarding here are both about what the builder must NOT
// do. It must store a relative period as the TOKEN rather than as the instant
// that token currently means — resolving at build time would freeze the view to
// the day it was saved, the same defect as substituting a user id for the "me"
// token. And it must raise the document version only when the document actually
// needs it, so a filter that stays inside v1's vocabulary stays readable by an
// older client.
describe('QueryFilterBuilder — v2 date ranges', () => {
  it('stores a relative preset as a token, not as a resolved instant', () => {
    const { last } = renderBuilder();

    fireEvent.change(screen.getByTestId('view-date-updated_at'), { target: { value: 'last-7d' } });

    expect(last().filter.updated_at).toEqual({ after: '-7d' });
    // The failure this rules out: an ISO instant here would look correct today
    // and silently stop meaning "last 7 days" tomorrow.
    expect(last().filter.updated_at!.after).not.toMatch(/\d{4}-\d{2}-\d{2}T/);
  });

  it('raises the document to v2 for a date range and lowers it again', () => {
    const { last } = renderBuilder();

    fireEvent.change(screen.getByTestId('view-date-created_at'), { target: { value: 'last-30d' } });
    expect(last().v).toBe(2);

    fireEvent.change(screen.getByTestId('view-date-created_at'), { target: { value: 'any' } });
    expect(last().filter.created_at).toBeUndefined();
    expect(last().v).toBe(1);
  });

  it('reveals two instant pickers only for a custom range', () => {
    renderBuilder();

    expect(screen.queryByTestId('view-date-due_at-after')).toBeNull();
    fireEvent.change(screen.getByTestId('view-date-due_at'), { target: { value: 'custom' } });
    expect(screen.getByTestId('view-date-due_at-after')).toBeInTheDocument();
    expect(screen.getByTestId('view-date-due_at-before')).toBeInTheDocument();
  });

  it('says plainly that a resolved-date filter matches nothing yet', () => {
    renderBuilder();

    fireEvent.change(screen.getByTestId('view-date-resolved_at'), { target: { value: 'last-7d' } });

    // Nothing in the product writes resolved_at, so the control must say so
    // rather than offer a filter that silently returns an empty list forever.
    expect(screen.getByText(/matches no items/)).toBeInTheDocument();
  });
});

describe('QueryFilterBuilder — v2 exclusion', () => {
  it('cannot be switched on for a field that names nothing', () => {
    renderBuilder();

    // "Everything except nothing" is everything, and the server refuses it —
    // so the control must be inert rather than able to build an unsaveable view.
    expect(screen.getByTestId('view-exclude-kinds')).toBeDisabled();
  });

  it('negates a field that has values, and raises the version', () => {
    const { last } = renderBuilder();

    fireEvent.click(screen.getByRole('button', { name: 'Bug' }));
    expect(last().v).toBe(1);

    fireEvent.click(screen.getByTestId('view-exclude-kinds'));

    expect(last().filter.not).toEqual({ kinds: true });
    // The values stay; only the sense flips.
    expect(last().filter.kinds).toEqual(['bug']);
    expect(last().v).toBe(2);
  });

  it('drops the exclusion when the last value is removed', () => {
    const { last } = renderBuilder();

    fireEvent.click(screen.getByRole('button', { name: 'Bug' }));
    fireEvent.click(screen.getByTestId('view-exclude-kinds'));
    expect(last().filter.not).toEqual({ kinds: true });

    fireEvent.click(screen.getByRole('button', { name: 'Bug' }));

    // A flag left behind over no values is a 422 the author cannot see the
    // cause of, because the toggle it came from is now disabled.
    expect(last().filter.kinds).toBeUndefined();
    expect(last().filter.not).toBeUndefined();
    expect(last().v).toBe(1);
  });

  it('warns that excluding an assignee keeps unassigned work in', () => {
    renderBuilder();

    fireEvent.click(screen.getByRole('button', { name: 'Me' }));
    fireEvent.click(screen.getByTestId('view-exclude-assignees'));

    expect(screen.getByText(/Work with no assignee is still included/)).toBeInTheDocument();
  });
});
