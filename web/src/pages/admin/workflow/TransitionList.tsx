import { useState } from 'react';
import { ArrowRight, ChevronDown, ChevronRight } from 'lucide-react';
import { cn } from '../../../lib/utils';
import {
  friendlyErrorMessage,
  useOrgWorkflowTransitions,
  type WorkflowState,
  type WorkflowTransition,
} from '../../../lib/api';
import { TransitionRules } from './TransitionRules';

/**
 * The transitions of one workflow, each expandable into its ADR-0011 rules.
 *
 * # Transitions are displayed faithfully, and not curated
 *
 * Every edge the workflow defines is listed, including the reverse edges
 * migration 029 added, which have never had a UI. They are shown exactly as
 * stored — no grouping that implies a pairing the data does not have, no
 * hiding of edges that look redundant, and no editing of the graph itself.
 * This surface configures RULES ON edges; creating and deleting edges is a
 * different job and is not part of it.
 *
 * # An unresolvable edge is shown, not skipped
 *
 * A transition whose from- or to-state is missing from the state list still
 * renders, naming the id it could not resolve. Skipping it would hide a
 * transition that the engine will still evaluate, and an admin cannot fix a
 * row they cannot see.
 */

interface TransitionListProps {
  orgId: string;
  workflowId: string;
  states: WorkflowState[];
}

export function TransitionList({ orgId, workflowId, states }: TransitionListProps) {
  const { data, isLoading, error } = useOrgWorkflowTransitions(orgId, workflowId);

  if (isLoading) {
    return (
      <p className="py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
        Loading transitions…
      </p>
    );
  }

  if (error) {
    return (
      <p
        data-testid="transitions-error"
        className="py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-danger)]"
      >
        {friendlyErrorMessage(error, 'Transitions could not be loaded.')}
      </p>
    );
  }

  const transitions = data ?? [];
  if (transitions.length === 0) {
    return (
      <p
        data-testid="no-transitions"
        className="py-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text-muted)]"
      >
        No transitions defined.
      </p>
    );
  }

  const nameOf = (stateId: string): string =>
    states.find((s) => s.id === stateId)?.name ?? `unknown state ${stateId.slice(0, 8)}`;

  return (
    <div className="space-y-1" data-testid={`transitions-${workflowId}`}>
      {transitions.map((t) => (
        <TransitionRow
          key={t.id}
          transition={t}
          orgId={orgId}
          workflowId={workflowId}
          fromName={nameOf(t.from_state_id)}
          toName={nameOf(t.to_state_id)}
        />
      ))}
    </div>
  );
}

function TransitionRow({
  transition,
  orgId,
  workflowId,
  fromName,
  toName,
}: {
  transition: WorkflowTransition;
  orgId: string;
  workflowId: string;
  fromName: string;
  toName: string;
}) {
  // Collapsed by default. Each expanded row costs three requests (guards,
  // approvers, post-functions), so expanding everything on mount would turn one
  // workflow into 1 + 3N round trips for a page whose usual visit reads a
  // single edge.
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="rounded-[var(--radius-md)] border border-[var(--color-border)]">
      <button
        type="button"
        data-testid={`transition-${transition.id}`}
        aria-expanded={expanded}
        onClick={() => setExpanded((p) => !p)}
        className={cn(
          'flex w-full items-center gap-[var(--space-2)] px-[var(--space-3)] py-[var(--space-2)]',
          'text-left text-[var(--text-sm)] text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]',
        )}
      >
        {expanded ? (
          <ChevronDown className="h-4 w-4 shrink-0 text-[var(--color-text-muted)]" />
        ) : (
          <ChevronRight className="h-4 w-4 shrink-0 text-[var(--color-text-muted)]" />
        )}
        <span className="font-medium">{transition.name}</span>
        <span className="ml-auto flex items-center gap-[var(--space-2)] text-[var(--text-xs)] text-[var(--color-text-muted)]">
          {fromName}
          <ArrowRight className="h-3 w-3" />
          {toName}
        </span>
      </button>

      {/* The rules load only once the row is opened. A closed row shows no
          counts and no badges — an untouched transition must not advertise an
          absence, and a count would have to be fetched to be shown. */}
      {expanded && (
        <div className="border-t border-[var(--color-border)] px-[var(--space-3)] py-[var(--space-3)]">
          <TransitionRules orgId={orgId} workflowId={workflowId} transitionId={transition.id} />
        </div>
      )}
    </div>
  );
}
