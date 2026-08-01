import type { WorkflowOffering } from '../api';

/**
 * The options a status picker should render for one entity.
 *
 * # Why this is shared rather than written twice
 *
 * Vector and Beacon both render a status `<select>`, and before this they each
 * carried their own hardcoded vocabulary. Those two lists disagreed with each
 * other, with the board's column list, and with the server — the vector picker
 * offered `closed`, which names no state in the seeded project workflow, and
 * omitted `todo`, which does. An item sitting in `todo` rendered a select whose
 * value was not among its own options.
 *
 * One derivation, used by both, is the only way that stays fixed.
 *
 * # The rules, and why each exists
 *
 * The CURRENT status is always included, even when the workflow does not offer
 * it. A `<select>` whose value is absent from its options renders blank in
 * React, and the user cannot even see where the item is — which is the bug the
 * vector page already had. No workflow defines an edge from a state to itself,
 * so the current status is never in the offered list and must be added here.
 *
 * A space with NO workflow falls back to the caller's own vocabulary. That is
 * not the same as a workflow that offers nothing, and the server distinguishes
 * them precisely so this can: collapsing the two would leave the picker with a
 * single option in a space that never had a workflow at all.
 *
 * While the offering is still LOADING the fallback is used too. The alternative
 * is a picker that flickers from five options to two on every page load, and a
 * brief over-offer is harmless — the server refuses anything illegal regardless,
 * which is the whole point of the enforcement this feeds.
 */
export interface StatusOption {
  value: string;
  /** True when choosing this asks for approval rather than moving the entity. */
  requiresApproval: boolean;
}

export function statusOptionsFor(
  currentStatus: string,
  offering: WorkflowOffering | undefined,
  fallback: readonly string[],
): StatusOption[] {
  const options: StatusOption[] = [{ value: currentStatus, requiresApproval: false }];

  if (!offering || offering.no_workflow) {
    for (const s of fallback) {
      if (s !== currentStatus) options.push({ value: s, requiresApproval: false });
    }
    return options;
  }

  for (const t of offering.transitions) {
    if (t.to_status === currentStatus) continue;
    options.push({ value: t.to_status, requiresApproval: t.requires_approval });
  }
  return options;
}

/**
 * The label for one option.
 *
 * The approval suffix is part of the label rather than a separate badge because
 * a native `<option>` cannot carry markup, and this is the only place the
 * distinction can be shown before the click. Without it the control silently
 * does two different things.
 */
export function statusOptionLabel(option: StatusOption, label: string): string {
  return option.requiresApproval ? `${label} (needs approval)` : label;
}
