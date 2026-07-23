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
}

/**
 * Item-type filter: one toggle chip per active type, matching the type-chip
 * vocabulary shipped in part 1. No selection means "all types" (every chip
 * reads unselected). Multi-select — the chips combine as an OR, so selecting
 * Bug and Story shows both. It only renders the control and reports the
 * selection; callers AND it with their other filters (search today, swimlane
 * grouping later), so there is no double-filtering here.
 */
export function TypeFilter({ options, selected, onToggle, className }: TypeFilterProps) {
  if (options.length === 0) return null;
  return (
    <div
      role="group"
      aria-label="Filter by type"
      data-testid="type-filter"
      className={cn('flex flex-wrap items-center gap-1.5', className)}
    >
      {options.map((opt) => {
        const active = selected.has(opt.slug);
        return (
          <button
            key={opt.slug}
            type="button"
            aria-pressed={active}
            onClick={() => onToggle(opt.slug)}
            className="rounded-full focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]"
          >
            <Badge variant={active ? 'default' : 'outline'}>{opt.name}</Badge>
          </button>
        );
      })}
    </div>
  );
}
