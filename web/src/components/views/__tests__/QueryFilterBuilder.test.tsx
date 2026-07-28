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
