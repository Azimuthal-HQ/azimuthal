import { describe, it, expect } from 'vitest';
import { statusOptionsFor, statusOptionLabel } from './statusOptions';
import type { WorkflowOffering } from '../api';

/**
 * The picker's option list is the client half of the two-part enforcement fix:
 * the server refuses illegal moves, and this stops the UI offering them.
 *
 * Each case below is a bug this repository has actually had, or one the shape of
 * the data makes easy to reintroduce.
 */

function offering(over: Partial<WorkflowOffering> = {}): WorkflowOffering {
  return {
    no_workflow: false,
    current_status: 'backlog',
    entity_status: 'backlog',
    transitions: [],
    ...over,
  };
}

describe('statusOptionsFor', () => {
  it('always includes the current status, which the workflow never offers', () => {
    // No workflow defines an edge from a state to itself, so the current status
    // is absent from every offering. A <select> whose value is not among its
    // options renders blank in React — the user cannot even see where the item
    // is. This is the bug the vector picker shipped with: ALL_STATUSES omitted
    // `todo`, so an item in `todo` had no matching option.
    const opts = statusOptionsFor('todo', offering({ current_status: 'todo' }), ['open']);
    expect(opts.map((o) => o.value)).toContain('todo');
  });

  it('offers exactly what the workflow offered, and nothing else', () => {
    const opts = statusOptionsFor(
      'backlog',
      offering({
        transitions: [
          { transition_id: 't1', name: 'Start', to_state_id: 's1', to_status: 'todo', requires_approval: false },
        ],
      }),
      ['open', 'in_progress', 'in_review', 'done', 'closed'],
    );
    expect(opts.map((o) => o.value)).toEqual(['backlog', 'todo']);
    expect(opts.map((o) => o.value)).not.toContain('done');
  });

  it('falls back to the caller vocabulary only when there is NO workflow', () => {
    // "No workflow, use your own statuses" and "the workflow offers you nothing"
    // are opposite instructions. Collapsing them would leave a picker with one
    // option in a space that never had a workflow at all.
    const noWorkflow = statusOptionsFor('open', offering({ no_workflow: true }), ['open', 'closed']);
    expect(noWorkflow.map((o) => o.value)).toEqual(['open', 'closed']);

    const workflowOffersNothing = statusOptionsFor('done', offering({ current_status: 'done' }), [
      'open',
      'closed',
    ]);
    expect(workflowOffersNothing.map((o) => o.value)).toEqual(['done']);
  });

  it('falls back while the offering is still loading', () => {
    // undefined is "not answered yet". Rendering one option until it arrives
    // would make the picker flicker on every page load, and over-offering is
    // harmless: the server refuses anything illegal regardless.
    const opts = statusOptionsFor('open', undefined, ['open', 'in_progress']);
    expect(opts.map((o) => o.value)).toEqual(['open', 'in_progress']);
  });

  it('never duplicates the current status', () => {
    // A workflow COULD carry a self-edge, and a duplicated <option> value is a
    // React key collision as well as a nonsense control.
    const opts = statusOptionsFor(
      'todo',
      offering({
        current_status: 'todo',
        transitions: [
          { transition_id: 't1', name: 'Loop', to_state_id: 's1', to_status: 'todo', requires_approval: false },
        ],
      }),
      [],
    );
    expect(opts.filter((o) => o.value === 'todo')).toHaveLength(1);
  });

  it('carries the approval flag through to the option', () => {
    const opts = statusOptionsFor(
      'backlog',
      offering({
        transitions: [
          { transition_id: 't1', name: 'Start', to_state_id: 's1', to_status: 'todo', requires_approval: true },
        ],
      }),
      [],
    );
    expect(opts.find((o) => o.value === 'todo')?.requiresApproval).toBe(true);
  });
});

describe('statusOptionLabel', () => {
  it('says so when a move needs approval, because a native option cannot', () => {
    // The distinction cannot be a badge — <option> takes no markup — and it is
    // the only warning before the click that this control will not move the
    // item but file a request.
    expect(statusOptionLabel({ value: 'todo', requiresApproval: true }, 'To Do')).toBe(
      'To Do (needs approval)',
    );
    expect(statusOptionLabel({ value: 'todo', requiresApproval: false }, 'To Do')).toBe('To Do');
  });
});
