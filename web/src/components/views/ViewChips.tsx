import { Compass, Globe, Lock, Users } from 'lucide-react';
import { Badge } from '../ui/badge';
import { cn } from '../../lib/utils';
import type { SavedView, ViewVisibility } from '../../lib/api';

/**
 * The saved-view chip vocabulary (P4, ADR-0009).
 *
 * All three are the shipped `Badge` chip, not a new marker. Spec §8 keeps two
 * channels apart — hue with matching text is STATE, hue background with neutral
 * text is PROVENANCE — and none of these is a state, so they all sit on the
 * neutral `secondary` / `outline` variants. In particular the invalid chip is
 * deliberately NOT `danger`: a view whose scope has gone is degraded, not
 * broken, and ADR-0009 case C1 says it must never read as an error.
 */

const VISIBILITY_ICON: Record<ViewVisibility, typeof Lock> = {
  private: Lock,
  team: Users,
  org: Globe,
};

const VISIBILITY_LABEL: Record<ViewVisibility, string> = {
  private: 'Private',
  team: 'Team',
  org: 'Organisation',
};

/**
 * Who can reach this view. Team visibility names its audience team; a team
 * visibility whose team has been deleted arrives with no `team_name`, and the
 * chip says so rather than rendering a bare "Team" that implies a team still
 * exists.
 */
export function ViewVisibilityChip({
  visibility,
  teamName,
  className,
}: {
  visibility: ViewVisibility;
  teamName?: string;
  className?: string;
}) {
  const Icon = VISIBILITY_ICON[visibility];
  const label =
    visibility === 'team'
      ? teamName
        ? `Team · ${teamName}`
        : 'Team · no longer exists'
      : VISIBILITY_LABEL[visibility];
  return (
    <Badge
      variant="secondary"
      data-testid="view-visibility-chip"
      data-visibility={visibility}
      className={cn('gap-1', className)}
    >
      <Icon className="h-3 w-3" aria-hidden />
      {label}
    </Badge>
  );
}

/**
 * Provenance: mine, or someone else's shared with me. `is_owner` is computed
 * server-side and is the only thing consulted — the client never compares an
 * owner id to the session.
 */
export function ViewOwnerChip({ view, className }: { view: SavedView; className?: string }) {
  if (view.is_owner) {
    return (
      <Badge variant="outline" data-testid="view-owner-chip" data-owner="you" className={className}>
        Yours
      </Badge>
    );
  }
  return (
    <Badge
      variant="outline"
      data-testid="view-owner-chip"
      data-owner="other"
      className={cn('gap-1', className)}
    >
      <Users className="h-3 w-3" aria-hidden />
      {view.owner_name ? `Shared by ${view.owner_name}` : 'Shared with you'}
    </Badge>
  );
}

/**
 * The scope-unavailable marker. Neutral by construction — see the note at the
 * top of this file.
 */
export function ViewScopeChip({ className }: { className?: string }) {
  return (
    <Badge
      variant="secondary"
      data-testid="view-scope-chip"
      className={cn('gap-1', className)}
    >
      <Compass className="h-3 w-3" aria-hidden />
      Scope unavailable
    </Badge>
  );
}

/**
 * The one sentence explaining a `is_valid: false` view, in the words the server
 * sent. `invalid_reason` is written for people; it is shown verbatim rather
 * than re-worded, and it is NOT an error message — it never travels through
 * friendlyErrorMessage, because nothing failed.
 */
export const SCOPE_UNAVAILABLE_FALLBACK =
  'The spaces or the audience this view was built on are no longer available.';

export function scopeUnavailableReason(view: SavedView): string {
  return view.invalid_reason?.trim() || SCOPE_UNAVAILABLE_FALLBACK;
}
