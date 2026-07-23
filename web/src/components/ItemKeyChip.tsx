import type { CSSProperties } from 'react';
import { cn } from '../lib/utils';
import type { ProjectItem } from '../lib/api';

type KeyBits = Pick<ProjectItem, 'item_key' | 'number' | 'id'>;

/**
 * itemKeyLabel resolves a project item's human-readable key. It prefers the
 * server-assigned, move-stable `item_key` and falls back to `<spaceKey>-<n>`
 * (for items fetched before the field existed) then a short id — so the label
 * is stable even mid-migration.
 */
export function itemKeyLabel(item: KeyBits, spaceKey?: string): string {
  if (item.item_key) return item.item_key;
  if (item.number) return `${spaceKey ?? 'PROJ'}-${item.number}`;
  return (item.id ?? '').slice(0, 8);
}

interface ItemKeyChipProps {
  item: KeyBits;
  spaceKey?: string;
  className?: string;
}

/**
 * ItemKeyChip renders a Vector item's key as a provenance chip (spec §8): the
 * Vector module hue at --module-chip-alpha for the background (via the
 * .module-chip rule) and neutral --module-chip-fg text, in the mono face. A key
 * marks where an item comes from, so it follows the provenance pattern
 * (hue background + neutral text) rather than a state pill.
 */
export function ItemKeyChip({ item, spaceKey, className }: ItemKeyChipProps) {
  return (
    <span
      data-testid="item-key-chip"
      className={cn(
        'module-chip inline-flex shrink-0 items-center rounded-[5px] px-[7px] py-[2px]',
        'text-[10px] font-medium leading-4',
        className,
      )}
      style={
        {
          '--chip-hue': 'var(--module-vector)',
          color: 'var(--module-chip-fg)',
          fontFamily: 'var(--font-mono)',
        } as CSSProperties
      }
    >
      {itemKeyLabel(item, spaceKey)}
    </span>
  );
}
