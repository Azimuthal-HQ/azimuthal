import type { LucideIcon } from 'lucide-react';
import { cn } from '../lib/utils';

interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  description: string;
  /** Optional call-to-action rendered under the description. */
  action?: React.ReactNode;
  className?: string;
}

/**
 * Branded empty state for routes and views with nothing to show yet.
 *
 * Every route must render either real content or this component — a blank
 * document body is never acceptable. Lives in the shell because P1's
 * navigation work adopts it across all modules.
 */
export function EmptyState({ icon: Icon, title, description, action, className }: EmptyStateProps) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center text-center',
        'rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]',
        'px-[var(--space-6)] py-[var(--space-12)]',
        className,
      )}
    >
      <div className="flex h-12 w-12 items-center justify-center rounded-[var(--radius-full)] bg-[var(--color-primary-muted)]">
        <Icon className="h-6 w-6 text-[var(--color-primary)]" />
      </div>
      <h2 className="mt-[var(--space-4)] text-[var(--text-lg)] font-semibold text-[var(--color-text)]">
        {title}
      </h2>
      <p className="mt-[var(--space-2)] max-w-md text-[var(--text-sm)] text-[var(--color-text-muted)]">
        {description}
      </p>
      {action && <div className="mt-[var(--space-4)]">{action}</div>}
    </div>
  );
}
