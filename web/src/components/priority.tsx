import type { SegmentedOption } from './ui/segmented';
import { cn } from '../lib/utils';

/**
 * The one priority vocabulary (interior prototype): the same colour-dot +
 * label marker in the create dialogs, detail views, boards, and lists.
 * Priority is state, so pills are tinted-background with matching foreground
 * (spec §8) — never a neutral-text chip.
 */
export type PriorityKey = 'low' | 'medium' | 'high' | 'critical';

export const PRIORITY_ORDER: PriorityKey[] = ['low', 'medium', 'high', 'critical'];

export const PRIORITY_LABEL: Record<PriorityKey, string> = {
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  critical: 'Critical',
};

/** Full-strength hue per level; tints derive from these via color-mix. */
const PRIORITY_HUE: Record<PriorityKey, string> = {
  low: 'var(--color-success)',
  medium: 'var(--color-primary)',
  high: 'var(--color-priority-high)',
  critical: 'var(--color-danger)',
};

const PRIORITY_PILL_CLASS: Record<PriorityKey, string> = {
  low: 'bg-[color-mix(in_srgb,var(--color-success)_16%,transparent)] text-[var(--color-success)]',
  medium:
    'bg-[color-mix(in_srgb,var(--color-primary)_16%,transparent)] text-[var(--color-primary)]',
  high: 'bg-[color-mix(in_srgb,var(--color-priority-high)_16%,transparent)] text-[var(--color-priority-high)]',
  critical:
    'bg-[color-mix(in_srgb,var(--color-danger)_18%,transparent)] text-[var(--color-danger)]',
};

const PRIORITY_SEGMENT_CLASS: Record<PriorityKey, string> = {
  low: 'bg-[color-mix(in_srgb,var(--color-success)_22%,transparent)] text-[var(--color-success)]',
  medium:
    'bg-[color-mix(in_srgb,var(--color-primary)_26%,transparent)] text-[var(--color-primary)]',
  high: 'bg-[color-mix(in_srgb,var(--color-priority-high)_24%,transparent)] text-[var(--color-priority-high)]',
  critical:
    'bg-[color-mix(in_srgb,var(--color-danger)_26%,transparent)] text-[var(--color-danger)]',
};

/** The wire spells Critical as 'urgent' (legacy); the UI never shows it. */
export const PRIORITY_TO_API: Record<PriorityKey, string> = {
  low: 'low',
  medium: 'medium',
  high: 'high',
  critical: 'urgent',
};

/**
 * Collapses the wire's priority spellings onto the vocabulary: the legacy
 * 'urgent' reads as Critical; anything unknown reads as Medium.
 */
export function normalizePriority(raw: string | null | undefined): PriorityKey {
  const key = String(raw ?? '').toLowerCase();
  if (key === 'urgent') return 'critical';
  return (PRIORITY_ORDER as string[]).includes(key) ? (key as PriorityKey) : 'medium';
}

export function PriorityDot({
  priority,
  className,
}: {
  priority: PriorityKey;
  className?: string;
}) {
  return (
    <span
      aria-hidden="true"
      className={cn('inline-block h-[7px] w-[7px] shrink-0 rounded-full', className)}
      style={{ backgroundColor: PRIORITY_HUE[priority] }}
    />
  );
}

/** The dot + label priority marker on tinted ground — boards, lists, details. */
export function PriorityPill({
  priority,
  className,
}: {
  priority: PriorityKey;
  className?: string;
}) {
  return (
    <span
      data-testid="priority-pill"
      data-priority={priority}
      className={cn(
        'inline-flex items-center gap-[5px] rounded-[5px] px-2 py-[3px] text-[11px] font-medium',
        PRIORITY_PILL_CLASS[priority],
        className,
      )}
    >
      <PriorityDot priority={priority} />
      {PRIORITY_LABEL[priority]}
    </span>
  );
}

/** The segmented-control options for the priority selector in create dialogs. */
export const PRIORITY_SEGMENT_OPTIONS: SegmentedOption<PriorityKey>[] =
  PRIORITY_ORDER.map((p) => ({
    value: p,
    label: PRIORITY_LABEL[p],
    dotColor: PRIORITY_HUE[p],
    selectedClassName: PRIORITY_SEGMENT_CLASS[p],
  }));
