import { useNavigate } from 'react-router-dom';
import { BookmarkPlus } from 'lucide-react';
import { Button } from '../ui/button';
import {
  VIEW_DRAFT_ROUTE,
  VIEW_DRAFT_STATE_KEY,
  type ViewDraft,
} from '../../lib/views/draft';

/**
 * The one "Save as view" control (P4, ADR-0009).
 *
 * A filterable list page hands it a draft built by the matching translator in
 * `lib/views/draft.ts`; this component only carries that draft to the builder.
 * It deliberately holds no filter knowledge of its own — a second control that
 * assembled its own QueryDoc would be the duplicate that shared-surfaces.md
 * exists to prevent.
 *
 * It stays enabled with no filters active: "everything in this space" is a
 * legitimate saved view, and a disabled control would read as a broken one.
 */
export function SaveAsViewButton({
  draft,
  className,
}: {
  draft: ViewDraft;
  className?: string;
}) {
  const navigate = useNavigate();

  return (
    <Button
      type="button"
      variant="outline"
      data-testid="save-as-view"
      className={className}
      onClick={() => navigate(VIEW_DRAFT_ROUTE, { state: { [VIEW_DRAFT_STATE_KEY]: draft } })}
    >
      <BookmarkPlus className="mr-2 h-4 w-4" />
      Save as view
    </Button>
  );
}
