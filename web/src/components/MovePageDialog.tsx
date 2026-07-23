import { useState } from 'react';
import { AlertTriangle } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
  Button,
} from './ui';
import {
  useSpaces,
  useMoveShareImpact,
  useMoveWikiPage,
  friendlyErrorMessage,
} from '../lib/api';

interface MovePageDialogProps {
  orgId: string;
  spaceId: string;
  pageId: string;
  pageTitle: string;
  onClose: () => void;
}

/**
 * MovePageDialog (P3, ADR-0008 rule 9). Moving a page to another space
 * revokes all of the moved subtree's shares — so before a cross-space move
 * the dialog shows how many active shares would be revoked. That count is
 * served by the API (never counted client-side), following the space-delete
 * confirmation pattern. Without this warning, a page shared org-wide could be
 * dragged into a sensitive space and stay public.
 */
export function MovePageDialog({ orgId, spaceId, pageId, pageTitle, onClose }: MovePageDialogProps) {
  const spacesQuery = useSpaces(orgId);
  const impact = useMoveShareImpact(orgId, spaceId, pageId);
  const move = useMoveWikiPage(spaceId, pageId);
  const [target, setTarget] = useState('');
  const [error, setError] = useState<string | null>(null);

  // Only Codex spaces can hold pages — the backend rejects any other module,
  // so offering Beacon/Vector here was a dead end dressed as a choice. The
  // caller must also be able to read the space, and the current space is
  // excluded (moving to it is a no-op here).
  const destinations = (spacesQuery.data ?? []).filter(
    (s) => s.type === 'codex' && s.readable !== false && s.id !== spaceId,
  );

  const activeShares = impact.data?.active_share_count ?? 0;
  const crossSpace = !!target && target !== spaceId;

  const submit = () => {
    setError(null);
    move.mutate(
      { parent_id: null, position: 0, target_space_id: target },
      {
        onSuccess: onClose,
        onError: (err) =>
          setError(
            friendlyErrorMessage(
              err,
              'The page could not be moved — check that the destination is a wiki you can edit, then try again.',
            ),
          ),
      },
    );
  };

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="move-page-dialog">
        <DialogHeader>
          <DialogTitle>Move “{pageTitle}”</DialogTitle>
          <DialogDescription>
            Move this page and everything beneath it to another space.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-1.5">
            <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]" htmlFor="move-target">
              Destination space
            </label>
            <select
              id="move-target"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
              data-testid="move-target-select"
              className="w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)] focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]"
            >
              <option value="">Choose a space…</option>
              {destinations.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>

          {/* The share-revocation warning: shown whenever the subtree carries
              active shares, and a cross-space move is selected. */}
          {crossSpace && activeShares > 0 && (
            <div
              className="flex items-start gap-2 rounded-[var(--radius-md)] border border-[var(--color-warning)] bg-[var(--color-warning)]/10 p-3"
              data-testid="move-share-warning"
            >
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-[var(--color-warning)]" aria-hidden />
              <p className="text-[var(--text-sm)] text-[var(--color-text)]">
                Moving this page will revoke {activeShares} active share
                {activeShares === 1 ? '' : 's'} on it and the pages beneath it. Anyone reading it
                through a share will lose access.
              </p>
            </div>
          )}

          {error && <p className="text-[var(--text-sm)] text-[var(--color-danger)]" data-testid="move-error">{error}</p>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} data-testid="move-cancel">
            Cancel
          </Button>
          <Button
            variant={crossSpace && activeShares > 0 ? 'destructive' : 'default'}
            disabled={!crossSpace || move.isPending}
            onClick={submit}
            data-testid="move-confirm"
          >
            {move.isPending ? 'Moving…' : crossSpace && activeShares > 0 ? 'Move and revoke shares' : 'Move'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
