import * as React from 'react';
import { cn } from '../../lib/utils';

export interface SegmentedOption<T extends string> {
  value: T;
  label: React.ReactNode;
  /** CSS color for the colour-dot variant (e.g. 'var(--color-success)'). */
  dotColor?: string;
  /** Selected-state tint override (the priority selector's per-level tint). */
  selectedClassName?: string;
}

interface SegmentedControlProps<T extends string> {
  options: SegmentedOption<T>[];
  value: T;
  onChange: (value: T) => void;
  'aria-label': string;
  className?: string;
  testId?: string;
  /** Equal-width options filling the container (default). Off for compact toggles. */
  fullWidth?: boolean;
  disabled?: boolean;
}

/**
 * One connected control for a small fixed set of exclusive choices (interior
 * prototype): equal-width segments, optional colour dot per option, radiogroup
 * keyboard semantics. The priority selector is one use; the user/team toggle
 * on grants is another. There must never be a second implementation.
 */
export function SegmentedControl<T extends string>({
  options,
  value,
  onChange,
  className,
  testId,
  fullWidth = true,
  disabled = false,
  'aria-label': ariaLabel,
}: SegmentedControlProps<T>) {
  const refs = React.useRef<Map<T, HTMLButtonElement>>(new Map());

  function moveTo(index: number) {
    const opt = options[(index + options.length) % options.length];
    onChange(opt.value);
    refs.current.get(opt.value)?.focus();
  }

  function handleKeyDown(e: React.KeyboardEvent, index: number) {
    switch (e.key) {
      case 'ArrowRight':
      case 'ArrowDown':
        e.preventDefault();
        moveTo(index + 1);
        break;
      case 'ArrowLeft':
      case 'ArrowUp':
        e.preventDefault();
        moveTo(index - 1);
        break;
      case 'Home':
        e.preventDefault();
        moveTo(0);
        break;
      case 'End':
        e.preventDefault();
        moveTo(options.length - 1);
        break;
    }
  }

  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      data-testid={testId}
      className={cn(
        'flex overflow-hidden rounded-[var(--radius-lg)] border border-[var(--color-border)]',
        fullWidth ? 'w-full' : 'w-auto',
        className,
      )}
    >
      {options.map((opt, i) => {
        const selected = opt.value === value;
        return (
          <button
            key={opt.value}
            ref={(el) => {
              if (el) refs.current.set(opt.value, el);
              else refs.current.delete(opt.value);
            }}
            type="button"
            role="radio"
            aria-checked={selected}
            tabIndex={selected ? 0 : -1}
            disabled={disabled}
            data-value={opt.value}
            onClick={() => onChange(opt.value)}
            onKeyDown={(e) => handleKeyDown(e, i)}
            className={cn(
              'flex h-9 items-center justify-center gap-1.5 px-3.5 text-[var(--text-sm)] font-medium transition-colors',
              'border-r border-[var(--color-border)] last:border-r-0',
              'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-[var(--color-primary)]',
              'disabled:cursor-not-allowed disabled:opacity-50',
              fullWidth && 'flex-1',
              selected
                ? (opt.selectedClassName ??
                  'bg-[var(--color-primary-muted)] text-[var(--color-primary)]')
                : 'bg-[var(--color-input)] text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
            )}
          >
            {opt.dotColor && (
              <span
                aria-hidden="true"
                className="h-[7px] w-[7px] shrink-0 rounded-full"
                style={{ backgroundColor: opt.dotColor }}
              />
            )}
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}
