import { Share2 } from 'lucide-react';
import { Badge } from './ui';
import { cn } from '../lib/utils';

interface ShareBadgeProps {
  /** Whether the entity is currently shared (or covered by a cascade). */
  shared: boolean;
  /** Extra hint, e.g. "org-wide" or "via a shared folder". */
  detail?: string;
  className?: string;
  testId?: string;
}

/**
 * ShareBadge (P3, ADR-0008 rule 5): a persistent, always-visible indicator on
 * any shared entity. It matters most under cascade — a page created inside a
 * shared folder is org-visible the moment it exists, and its author must see
 * that before typing. Rendered only when `shared` is true; the caller decides
 * shared-ness (a direct share, or cascade coverage from an ancestor).
 */
export function ShareBadge({ shared, detail, className, testId = 'share-badge' }: ShareBadgeProps) {
  if (!shared) return null;
  return (
    <Badge
      variant="warning"
      className={cn('gap-1', className)}
      data-testid={testId}
      title={detail ? `Shared — ${detail}` : 'Shared'}
    >
      <Share2 className="h-3 w-3" aria-hidden />
      Shared{detail ? ` · ${detail}` : ''}
    </Badge>
  );
}
