import * as React from 'react';
import { cn } from '../../lib/utils';

/** One form field: label, control, optional hint (interior prototype). */
export function Field({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('mb-4 last:mb-0', className)} {...props} />;
}

interface FieldLabelProps extends React.LabelHTMLAttributes<HTMLLabelElement> {
  /** Appends the muted "optional" marker. */
  optional?: boolean;
}

export function FieldLabel({
  className,
  optional,
  children,
  ...props
}: FieldLabelProps) {
  return (
    <label
      className={cn(
        'mb-1.5 block text-[var(--text-xs)] font-medium text-[var(--color-text)]',
        className,
      )}
      {...props}
    >
      {children}
      {optional && (
        <span className="ml-1.5 font-normal text-[var(--color-text-muted)]">
          optional
        </span>
      )}
    </label>
  );
}

export function FieldHint({
  className,
  ...props
}: React.HTMLAttributes<HTMLParagraphElement>) {
  return (
    <p
      className={cn(
        'mt-1.5 text-[11px] leading-4 text-[var(--color-text-muted)]',
        className,
      )}
      {...props}
    />
  );
}
