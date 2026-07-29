import { Link } from 'react-router-dom';
import { Compass } from 'lucide-react';
import { Button } from '../ui/button';
import { EmptyState } from '../../shell/EmptyState';
import { scopeUnavailableReason } from './ViewChips';
import type { SavedView } from '../../lib/api';

/**
 * The "scope unavailable" state of a saved view (ADR-0009 case C1).
 *
 * A view goes invalid when the thing it was scoped to disappears — its spaces
 * were deleted, or the team it was shared with was. The view itself is intact:
 * it still lists, it still opens, and its definition is still editable. So this
 * is the branded EmptyState with an action, never the danger panel and never a
 * friendlyErrorMessage — nothing failed, and presenting it as a failure would
 * push the owner towards deleting a view they only need to re-scope.
 *
 * Only the owner is offered the fix, because only the owner may edit it.
 */
export function ScopeUnavailable({ view }: { view: SavedView }) {
  return (
    <div data-testid="view-scope-unavailable">
      <EmptyState
        icon={Compass}
        title="Scope unavailable"
        description={scopeUnavailableReason(view)}
        action={
          view.is_owner ? (
            <Button asChild>
              <Link to={`/views/${view.id}/edit`}>Re-scope this view</Link>
            </Button>
          ) : (
            <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
              {view.owner_name
                ? `${view.owner_name} can point it somewhere that still exists.`
                : 'Its owner can point it somewhere that still exists.'}
            </p>
          )
        }
      />
    </div>
  );
}
