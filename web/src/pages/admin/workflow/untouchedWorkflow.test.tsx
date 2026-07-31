import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { TransitionList } from './TransitionList';
import type {
  WorkflowApprover,
  WorkflowGuard,
  WorkflowPostFunction,
  WorkflowState,
  WorkflowTransition,
} from '../../../lib/api';

/**
 * The UI half of the untouched-workflow guarantee.
 *
 * PR #86 made a promise at the service layer and proved it there:
 * TestGate_UntouchedWorkflowIsUnaffected walks all four ways of not being
 * configured and shows each one answers "nothing applies". This PR extends the
 * same promise to the screen, because an administrator who has configured
 * nothing must not be able to TELL that the feature shipped. A page announcing
 * an absence — "0 guards", "Unrestricted", an empty rules table, a dashed
 * "Set up approvals" box — is a report that something changed about their
 * workflow, and nothing did.
 *
 * The one deliberate exception is the "Add a rule" affordance. The phase brief
 * requires per-transition editing, which cannot be delivered invisibly; a
 * control that offers to create something is not a claim that something is
 * missing. Everything else is held to silence, and this file is what holds it.
 *
 * # Why this is asserted by absence, unusually
 *
 * An absence assertion is normally vacuous — CLAUDE.md §2's negative-test
 * question applies hard — so the configured case below renders the SAME
 * component with rules present and asserts every one of those strings DOES
 * appear. Each absence is therefore paired with a sighting: the words exist in
 * the component, and their non-appearance when nothing is configured is a
 * property of the data rather than of a typo in the test.
 */

let guards: WorkflowGuard[] = [];
let approvers: WorkflowApprover[] = [];
let postFunctions: WorkflowPostFunction[] = [];

const transition: WorkflowTransition = {
  id: 't1',
  workflow_id: 'w1',
  from_state_id: 's1',
  to_state_id: 's2',
  name: 'Start Progress',
};

const states: WorkflowState[] = [
  { id: 's1', workflow_id: 'w1', name: 'open', category: 'todo', color: '#1', position: 0, is_initial: true, created_at: '' },
  { id: 's2', workflow_id: 'w1', name: 'in_progress', category: 'in_progress', color: '#2', position: 1, is_initial: false, created_at: '' },
];

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useOrgWorkflowTransitions: () => ({ data: [transition], isLoading: false, error: null }),
    useTransitionGuards: () => ({ data: guards, isLoading: false, error: null }),
    useTransitionApprovers: () => ({ data: approvers, isLoading: false, error: null }),
    useTransitionPostFunctions: () => ({ data: postFunctions, isLoading: false, error: null }),
    useTeams: () => ({ data: [] }),
    useMemberSearch: () => ({ data: [], isLoading: false }),
    useCreateTransitionGuard: () => ({ mutate: vi.fn(), isPending: false, error: null }),
    useDeleteTransitionGuard: () => ({ mutate: vi.fn(), isPending: false, error: null }),
    useCreateTransitionApprover: () => ({ mutate: vi.fn(), isPending: false, error: null }),
    useDeleteTransitionApprover: () => ({ mutate: vi.fn(), isPending: false, error: null }),
    useCreateTransitionPostFunction: () => ({ mutate: vi.fn(), isPending: false, error: null }),
    useDeleteTransitionPostFunction: () => ({ mutate: vi.fn(), isPending: false, error: null }),
  };
});

beforeEach(() => {
  guards = [];
  approvers = [];
  postFunctions = [];
});

function renderList() {
  return render(<TransitionList orgId="o1" workflowId="w1" states={states} />);
}

/** Expand the transition so its rules region is in the tree at all. */
function openTransition() {
  fireEvent.click(screen.getByTestId('transition-t1'));
}

describe('a workflow with no tiers configured says nothing about tiers', () => {
  it('lists the transition faithfully, with its real endpoints', () => {
    renderList();
    expect(screen.getByTestId('transition-t1')).toBeInTheDocument();
    expect(screen.getByText('Start Progress')).toBeInTheDocument();
    // Reverse edges (migration 029) have no UI of their own and are displayed
    // exactly as stored — the endpoints are shown, not interpreted. Read off
    // the row's text rather than by exact node, because the two state names
    // share one element with the arrow between them.
    expect(screen.getByTestId('transition-t1')).toHaveTextContent('open');
    expect(screen.getByTestId('transition-t1')).toHaveTextContent('in_progress');
  });

  it('shows an unresolvable endpoint rather than hiding the transition', () => {
    // A transition whose state is missing from the state list still renders,
    // naming the id it could not resolve. Skipping it would hide an edge the
    // engine will still evaluate, and an admin cannot fix a row they cannot
    // see.
    render(<TransitionList orgId="o1" workflowId="w1" states={[]} />);
    expect(screen.getByTestId('transition-t1')).toBeInTheDocument();
    expect(screen.getByTestId('transition-t1')).toHaveTextContent(/unknown state/i);
  });

  it('shows no rule chrome on a closed transition', () => {
    renderList();
    // A closed row must not advertise a count, which would also mean fetching
    // three collections to render a page nobody asked to configure.
    expect(screen.queryByTestId('transition-rules-t1')).not.toBeInTheDocument();
    expect(screen.queryByText(/conditions and validators/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/approvers/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/actions on success/i)).not.toBeInTheDocument();
  });

  it('shows no empty tables, counts, or "unrestricted" labels when opened', () => {
    renderList();
    openTransition();

    expect(screen.getByTestId('transition-rules-t1')).toBeInTheDocument();

    // The three group headings are the empty tables. None may render.
    expect(screen.queryByText(/conditions and validators/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^approvers$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/actions on success/i)).not.toBeInTheDocument();

    // And none of the vocabulary of absence.
    expect(screen.queryByText(/unrestricted/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/no restrictions/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/\b0 (guards|rules|approvers)\b/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/none configured/i)).not.toBeInTheDocument();
  });

  it('offers the editing affordance, which is the one deliberate exception', () => {
    renderList();
    openTransition();
    // A control that offers to CREATE something is not a claim that something
    // is missing, and the phase cannot deliver per-transition editing without
    // one. Named here so the exception is explicit rather than an oversight.
    expect(screen.getByTestId('add-guard-t1')).toBeInTheDocument();
    expect(screen.getByTestId('add-approver-t1')).toBeInTheDocument();
    expect(screen.getByTestId('add-post-function-t1')).toBeInTheDocument();
  });
});

describe('the same component does render all of it once something is configured', () => {
  // This is what stops every assertion above from being vacuous. If the group
  // headings were misspelled in the component, the absences would pass and this
  // would fail.
  beforeEach(() => {
    guards = [
      { id: 'g1', transition_id: 't1', guard_class: 'validator', kind: 'field_required', position: 0, field_key: 'assignee_id' },
    ];
    approvers = [
      { id: 'a1', transition_id: 't1', subject_type: 'user', subject_id: 'u1', subject_name: 'Dana' },
    ];
    postFunctions = [
      { id: 'p1', transition_id: 't1', kind: 'set_field', position: 0, field_key: 'labels', field_value: 'urgent' },
    ];
  });

  it('renders each group and each rule', () => {
    renderList();
    openTransition();

    expect(screen.getByText(/conditions and validators/i)).toBeInTheDocument();
    expect(screen.getByText(/^approvers$/i)).toBeInTheDocument();
    expect(screen.getByText(/actions on success/i)).toBeInTheDocument();

    expect(screen.getByTestId('guard-g1')).toBeInTheDocument();
    expect(screen.getByText(/assignee must be filled in/i)).toBeInTheDocument();
    expect(screen.getByTestId('approver-a1')).toBeInTheDocument();
    expect(screen.getByText('Dana')).toBeInTheDocument();
    expect(screen.getByTestId('post-function-p1')).toBeInTheDocument();
  });
});

describe('a degraded guard reads as needing attention, never as no restriction', () => {
  beforeEach(() => {
    // migration 046 sets team_id NULL when the team is deleted, so the guard
    // survives as UNSATISFIABLE rather than silently disappearing. An admin
    // shown "no restriction" here would be reading the exact opposite of what
    // the engine will do.
    guards = [
      { id: 'g2', transition_id: 't1', guard_class: 'validator', kind: 'actor_in_team', position: 0 },
    ];
  });

  it('names the missing team and says to re-add it', () => {
    renderList();
    openTransition();

    expect(screen.getByTestId('guard-g2')).toBeInTheDocument();
    expect(screen.getByText(/team that no longer exists/i)).toBeInTheDocument();
    // Re-scoped, never resubmitted: ValidateGuard refuses a null team on WRITE
    // even though the database holds one at rest, so a read-modify-write round
    // trip on this row fails.
    expect(screen.getByText(/add it again with a current team/i)).toBeInTheDocument();
  });
});
