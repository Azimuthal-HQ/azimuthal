import * as React from 'react';
import { cn } from '../../lib/utils';

export interface RadioCardOption<T extends string> {
  value: T;
  title: string;
  description: string;
  disabled?: boolean;
}

interface RadioCardGroupProps<T extends string> {
  options: RadioCardOption<T>[];
  value: T | undefined;
  onChange: (value: T) => void;
  'aria-label': string;
  className?: string;
  testId?: string;
}

/**
 * Choose-one-of-few-with-descriptions (interior prototype): each option is a
 * bordered card with a radio, a title, and a describing line. Radiogroup
 * keyboard semantics; the selected card carries the accent border and tint.
 */
export function RadioCardGroup<T extends string>({
  options,
  value,
  onChange,
  className,
  testId,
  'aria-label': ariaLabel,
}: RadioCardGroupProps<T>) {
  const refs = React.useRef<Map<T, HTMLDivElement>>(new Map());

  function moveTo(index: number) {
    const enabled = options.filter((o) => !o.disabled);
    if (enabled.length === 0) return;
    const opt = enabled[((index % enabled.length) + enabled.length) % enabled.length];
    onChange(opt.value);
    refs.current.get(opt.value)?.focus();
  }

  function handleKeyDown(e: React.KeyboardEvent, opt: RadioCardOption<T>) {
    const enabled = options.filter((o) => !o.disabled);
    const index = enabled.findIndex((o) => o.value === opt.value);
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
      case ' ':
      case 'Enter':
        e.preventDefault();
        onChange(opt.value);
        break;
    }
  }

  const tabbable = value ?? options.find((o) => !o.disabled)?.value;

  return (
    <div
      role="radiogroup"
      aria-label={ariaLabel}
      data-testid={testId}
      className={cn('flex flex-col gap-2', className)}
    >
      {options.map((opt) => {
        const selected = opt.value === value;
        return (
          <div
            key={opt.value}
            ref={(el) => {
              if (el) refs.current.set(opt.value, el);
              else refs.current.delete(opt.value);
            }}
            role="radio"
            aria-checked={selected}
            aria-disabled={opt.disabled || undefined}
            tabIndex={opt.disabled ? -1 : opt.value === tabbable ? 0 : -1}
            data-value={opt.value}
            onClick={() => !opt.disabled && onChange(opt.value)}
            onKeyDown={(e) => !opt.disabled && handleKeyDown(e, opt)}
            className={cn(
              'flex cursor-pointer gap-3 rounded-[var(--radius-lg)] border p-3 transition-colors',
              'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
              selected
                ? 'border-[var(--color-primary)] bg-[color-mix(in_srgb,var(--color-primary)_6%,transparent)]'
                : 'border-[var(--color-border)] hover:border-[var(--color-text-muted)]',
              opt.disabled && 'cursor-not-allowed opacity-50',
            )}
          >
            <span
              aria-hidden="true"
              className={cn(
                'relative mt-0.5 h-[17px] w-[17px] shrink-0 rounded-full border-[1.5px]',
                selected ? 'border-[var(--color-primary)]' : 'border-[var(--color-border)]',
              )}
            >
              {selected && (
                <span className="absolute inset-[3px] rounded-full bg-[var(--color-primary)]" />
              )}
            </span>
            <span className="min-w-0">
              <span
                className={cn(
                  'block text-[var(--text-sm)] font-medium',
                  selected ? 'text-[var(--color-primary)]' : 'text-[var(--color-text)]',
                )}
              >
                {opt.title}
              </span>
              <span className="block text-[var(--text-xs)] leading-5 text-[var(--color-text-muted)]">
                {opt.description}
              </span>
            </span>
          </div>
        );
      })}
    </div>
  );
}
