import { Filter, X } from 'lucide-react';
import { cn } from '../lib/utils';
import { useTeamFocus } from './hooks/useTeamFocus';

/**
 * FocusChip surfaces the active team focus in the top bar with one-click
 * clear (ADR-0006 point 7). Rendered only while a focus is active; with
 * teams arriving in P2, useTeamFocus reports null and the chip stays out
 * of the tree entirely.
 */
export function FocusChip({ className }: { className?: string }) {
  const { focus, clearFocus } = useTeamFocus();
  if (!focus) return null;

  return (
    <span
      data-testid="focus-chip"
      className={cn(
        'flex h-7 items-center gap-[var(--space-2)] rounded-[var(--radius-full)]',
        'bg-[var(--color-primary-muted)] pl-[var(--space-3)] pr-[var(--space-1)]',
        'text-[var(--text-xs)] font-medium text-[var(--color-primary)] whitespace-nowrap',
        className,
      )}
    >
      <Filter className="h-3.5 w-3.5" />
      {focus.teamName}
      <button
        type="button"
        onClick={clearFocus}
        aria-label="Clear team focus"
        className={cn(
          'inline-flex h-5 w-5 items-center justify-center rounded-[var(--radius-full)]',
          'opacity-80 hover:opacity-100 hover:bg-[var(--color-surface-hover)]',
          'transition-opacity duration-150',
        )}
      >
        <X className="h-3.5 w-3.5" />
      </button>
    </span>
  );
}
