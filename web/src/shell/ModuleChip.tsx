import type { CSSProperties } from 'react';
import { cn } from '../lib/utils';
import { MODULES, type ModuleKey } from './modules';

interface ModuleChipProps {
  module: ModuleKey;
  className?: string;
}

/**
 * ModuleChip is the provenance chip (spec §8): background is the module hue
 * at --module-chip-alpha (via the .module-chip rule), icon and text are
 * --module-chip-fg. The foreground is never the module hue — hue with
 * matching text would read as state, and provenance and state must stay
 * distinct channels.
 */
export function ModuleChip({ module, className }: ModuleChipProps) {
  const def = MODULES[module];
  return (
    <span
      data-testid="module-chip"
      data-module={module}
      className={cn(
        'module-chip inline-flex shrink-0 items-center rounded-[5px] px-[7px] py-[2px]',
        'text-[10px] font-medium leading-4',
        className,
      )}
      style={{ '--chip-hue': `var(${def.hueVar})`, color: 'var(--module-chip-fg)' } as CSSProperties}
    >
      {def.name}
    </span>
  );
}
