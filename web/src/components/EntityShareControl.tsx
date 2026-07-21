import { useState } from 'react';
import { Share2 } from 'lucide-react';
import { Button } from './ui';
import { ShareBadge } from './ShareBadge';
import { ShareDialog } from './ShareDialog';
import {
  useEffectiveAccess,
  useEntityShares,
  type ShareEntityType,
} from '../lib/api';

interface EntityShareControlProps {
  orgId: string;
  spaceId: string;
  entityType: ShareEntityType;
  entityId: string;
  entityLabel: string;
}

/**
 * EntityShareControl bundles the Share button, the ShareBadge, and the
 * ShareDialog for a flat entity (ticket or project item). Both the button and
 * the badge are gated on manage_shares — only a space admin creates shares,
 * and the share list read is itself capability-guarded, so a non-admin sees
 * neither. (Pages use the space-read badge endpoint instead, so their author
 * sees cascade coverage without manage_shares — see WikiPage.)
 */
export function EntityShareControl({
  orgId,
  spaceId,
  entityType,
  entityId,
  entityLabel,
}: EntityShareControlProps) {
  const { data: eff } = useEffectiveAccess(orgId, spaceId, undefined, { enabled: !!orgId && !!spaceId });
  const canManageShares = eff?.org_admin || eff?.role === 'space_admin';
  const shares = useEntityShares(orgId, entityType, entityId, {
    enabled: !!orgId && !!entityId && !!canManageShares,
  });
  const [open, setOpen] = useState(false);

  if (!canManageShares) return null;

  const active = (shares.data?.shares ?? []).some((s) => !s.expired);

  return (
    <div className="flex items-center gap-2" data-testid="entity-share-control">
      <ShareBadge shared={active} />
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)} data-testid="entity-share-button">
        <Share2 className="mr-1.5 h-3.5 w-3.5" />
        Share
      </Button>
      {open && (
        <ShareDialog
          orgId={orgId}
          entityType={entityType}
          entityId={entityId}
          entityLabel={entityLabel}
          onClose={() => setOpen(false)}
        />
      )}
    </div>
  );
}
