import * as React from 'react';
import { cn } from '../../lib/utils';

/**
 * Two-column detail layout (interior prototype): content left, properties
 * right, one bordered card. Collapses to a single column below lg.
 */
export function DetailLayout({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        'grid grid-cols-1 overflow-hidden rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)] lg:grid-cols-[minmax(0,1fr)_280px]',
        className,
      )}
      {...props}
    />
  );
}

export function DetailMain({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        'min-w-0 border-b border-[var(--color-border)] p-5 lg:border-b-0 lg:border-r',
        className,
      )}
      {...props}
    />
  );
}

export function DetailSide({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('p-[18px]', className)} {...props} />;
}

interface DetailFieldProps extends React.HTMLAttributes<HTMLDivElement> {
  label: React.ReactNode;
}

/** One properties-panel row: uppercase micro-label over the value. */
export function DetailField({
  label,
  className,
  children,
  ...props
}: DetailFieldProps) {
  return (
    <div className={cn('mb-4 last:mb-0', className)} {...props}>
      <div className="mb-1.5 text-[10.5px] uppercase tracking-[.04em] text-[var(--color-text-muted)]">
        {label}
      </div>
      {children}
    </div>
  );
}

export function DetailDivider({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('my-4 h-px bg-[var(--color-border)]', className)}
      {...props}
    />
  );
}
