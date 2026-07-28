import { Badge } from './ui/badge';
import { cn } from '../lib/utils';

export interface TypeFilterOption {
  slug: string;
  name: string;
}

interface TypeFilterProps {
  options: TypeFilterOption[];
  /** Selected type slugs. An empty set means "all types". */
  selected: Set<string>;
  onToggle: (slug: string) => void;
  className?: string;
  /** Group label. Defaults to the item-type wording this control shipped with. */
  label?: string;
  /** data-testid override, so two groups on one page stay distinguishable. */
  testId?: string;
  /** Renders every chip inert — used where a selection is not permitted yet. */
  disabled?: boolean;
}

/**
 * The multi-select chip group: one toggle chip per option, no selection meaning
 * "all of them" (every chip reads unselected). Multi-select — the chips combine
 * as an OR, so selecting Bug and Story shows both. It only renders the control
 * and reports the selection; callers AND it with their other filters, so there
 * is no double-filtering here.
 *
 * Item types were its first and are still its default use — hence the name and
 * the default label. P4's view builder reuses it for every closed-vocabulary
 * multi-select it offers (modules, spaces, priorities, types, sprints) rather
 * than growing a second chip-toggle implementation beside it, which is why
 * `label` and `testId` are parameters instead of constants.
 */
export function TypeFilter({
  options,
  selected,
  onToggle,
  className,
  label = 'Filter by type',
  testId = 'type-filter',
  disabled = false,
}: TypeFilterProps) {
  if (options.length === 0) return null;
  return (
    <div
      role="group"
      aria-label={label}
      data-testid={testId}
      className={cn('flex flex-wrap items-center gap-1.5', className)}
    >
      {options.map((opt) => {
        const active = selected.has(opt.slug);
        return (
          <button
            key={opt.slug}
            type="button"
            aria-pressed={active}
            disabled={disabled}
            onClick={() => onToggle(opt.slug)}
            className={cn(
              'rounded-full focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
              disabled && 'cursor-not-allowed opacity-50',
            )}
          >
            <Badge variant={active ? 'default' : 'outline'}>{opt.name}</Badge>
          </button>
        );
      })}
    </div>
  );
}
