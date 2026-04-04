import * as React from 'react';
import { cva, type VariantProps } from 'class-variance-authority';
import { cn } from '../../lib/utils';

const badgeVariants = cva(
  'inline-flex items-center rounded-[var(--radius-full)] px-2.5 py-0.5 text-[var(--text-xs)] font-medium transition-colors',
  {
    variants: {
      variant: {
        default: 'bg-[var(--color-primary)] text-white',
        secondary:
          'bg-[var(--color-surface-hover)] text-[var(--color-text)]',
        success: 'bg-[var(--color-success)] text-white',
        warning: 'bg-[var(--color-warning)] text-[var(--color-text-inverse)]',
        danger: 'bg-[var(--color-danger)] text-white',
        outline:
          'border border-[var(--color-border)] text-[var(--color-text)] bg-transparent',
      },
    },
    defaultVariants: {
      variant: 'default',
    },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

const Badge = React.forwardRef<HTMLDivElement, BadgeProps>(
  ({ className, variant, ...props }, ref) => {
    return (
      <div
        className={cn(badgeVariants({ variant, className }))}
        ref={ref}
        {...props}
      />
    );
  },
);
Badge.displayName = 'Badge';

export { Badge, badgeVariants };
