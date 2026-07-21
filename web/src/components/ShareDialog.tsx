import { useState } from 'react';
import { Trash2 } from 'lucide-react';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogFooter,
  DialogTitle,
  DialogDescription,
  Button,
  Input,
  Badge,
} from './ui';
import { PersonTeamPicker, type PickedSubject } from './PersonTeamPicker';
import {
  useEntityShares,
  useCreateShare,
  useRevokeShare,
  friendlyErrorMessage,
  type ShareEntityType,
  type Share,
} from '../lib/api';

interface ShareDialogProps {
  orgId: string;
  entityType: ShareEntityType;
  entityId: string;
  entityLabel: string;
  onClose: () => void;
}

/**
 * ShareDialog (P3, ADR-0008). Only manage_shares (space admins) reach it. A
 * share is data, not policy: the operator chooses an audience (org-wide, or a
 * specific team), whether it cascades (pages only, single entity by default),
 * and an optional expiry. Cascade shows the affected-page count BEFORE
 * confirming, served by the API (never counted client-side). The recipient
 * picker for team audiences reuses PersonTeamPicker — no second picker.
 */
export function ShareDialog({ orgId, entityType, entityId, entityLabel, onClose }: ShareDialogProps) {
  const sharesQuery = useEntityShares(orgId, entityType, entityId);
  const createShare = useCreateShare(orgId, entityType, entityId);
  const revokeShare = useRevokeShare(orgId, entityType, entityId);

  const [audience, setAudience] = useState<'org' | 'team'>('org');
  const [team, setTeam] = useState<PickedSubject | null>(null);
  const [cascade, setCascade] = useState(false);
  const [expiresAt, setExpiresAt] = useState('');
  const [error, setError] = useState<string | null>(null);

  const isPage = entityType === 'page';
  const shares = sharesQuery.data?.shares ?? [];
  const cascadeCount = sharesQuery.data?.cascade_page_count ?? 0;

  const submit = () => {
    setError(null);
    createShare.mutate(
      {
        entity_type: entityType,
        entity_id: entityId,
        audience,
        audience_id: audience === 'team' ? team?.id : undefined,
        cascade: isPage ? cascade : undefined,
        // datetime-local yields "YYYY-MM-DDTHH:mm"; send an RFC3339 instant.
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : undefined,
      },
      {
        onSuccess: () => {
          setTeam(null);
          setCascade(false);
          setExpiresAt('');
        },
        onError: (err) => setError(friendlyErrorMessage(err, 'The share could not be created.')),
      },
    );
  };

  const canSubmit = !createShare.isPending && (audience === 'org' || !!team);

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="share-dialog">
        <DialogHeader>
          <DialogTitle>Share “{entityLabel}”</DialogTitle>
          <DialogDescription>
            Sharing widens who can read this {entityTypeLabel(entityType)} — it is always read-only,
            and never reveals its space, tree, or comments.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {/* Audience */}
          <div className="space-y-1.5">
            <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Audience</label>
            <div className="flex gap-2" role="radiogroup" aria-label="Share audience">
              <AudienceOption
                label="Everyone in the org"
                active={audience === 'org'}
                onClick={() => setAudience('org')}
                testId="share-audience-org"
              />
              <AudienceOption
                label="A specific team"
                active={audience === 'team'}
                onClick={() => setAudience('team')}
                testId="share-audience-team"
              />
            </div>
            {audience === 'team' && (
              <div className="pt-1">
                <PersonTeamPicker
                  orgId={orgId}
                  subjects="team"
                  value={team}
                  onChange={setTeam}
                  placeholder="Search teams…"
                  testId="share-team-picker"
                />
              </div>
            )}
          </div>

          {/* Cascade (pages only) */}
          {isPage && (
            <label className="flex items-start gap-2" data-testid="share-cascade">
              <input
                type="checkbox"
                className="mt-1"
                checked={cascade}
                onChange={(e) => setCascade(e.target.checked)}
                data-testid="share-cascade-checkbox"
              />
              <span className="text-[var(--text-sm)] text-[var(--color-text)]">
                Include every page beneath this one
                {cascade && (
                  <span className="block text-[var(--text-xs)] text-[var(--color-warning)]" data-testid="share-cascade-count">
                    This will share {cascadeCount} page{cascadeCount === 1 ? '' : 's'}, including any
                    added later.
                  </span>
                )}
              </span>
            </label>
          )}

          {/* Optional expiry */}
          <div className="space-y-1.5">
            <label className="text-[var(--text-sm)] font-medium text-[var(--color-text)]" htmlFor="share-expiry">
              Expires (optional)
            </label>
            <Input
              id="share-expiry"
              type="datetime-local"
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
              data-testid="share-expiry-input"
            />
          </div>

          {error && (
            <p className="text-[var(--text-sm)] text-[var(--color-danger)]" data-testid="share-error">
              {error}
            </p>
          )}

          <Button onClick={submit} disabled={!canSubmit} data-testid="share-submit">
            {createShare.isPending ? 'Sharing…' : 'Share'}
          </Button>

          {/* Existing shares, revocable */}
          <div className="space-y-2 border-t border-[var(--color-border)] pt-3">
            <p className="text-[var(--text-sm)] font-medium text-[var(--color-text)]">Active shares</p>
            {sharesQuery.isLoading && (
              <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading…</p>
            )}
            {!sharesQuery.isLoading && shares.length === 0 && (
              <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]" data-testid="share-empty">
                Not shared with anyone yet.
              </p>
            )}
            {shares.map((s) => (
              <ShareRow
                key={s.id}
                share={s}
                onRevoke={() =>
                  revokeShare.mutate(s.id, {
                    onError: (err) => setError(friendlyErrorMessage(err, 'The share could not be revoked.')),
                  })
                }
                revoking={revokeShare.isPending}
              />
            ))}
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} data-testid="share-close">
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function AudienceOption({
  label,
  active,
  onClick,
  testId,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
  testId: string;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={active}
      onClick={onClick}
      data-testid={testId}
      className={
        'flex-1 rounded-[var(--radius-md)] border px-3 py-2 text-[var(--text-sm)] transition-colors ' +
        (active
          ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)] text-[var(--color-primary)]'
          : 'border-[var(--color-border)] text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]')
      }
    >
      {label}
    </button>
  );
}

function ShareRow({ share, onRevoke, revoking }: { share: Share; onRevoke: () => void; revoking: boolean }) {
  const audienceText = share.audience === 'org' ? 'Everyone in the org' : 'A team';
  return (
    <div className="flex items-center justify-between gap-2" data-testid="share-row">
      <div className="flex items-center gap-2">
        <span className="text-[var(--text-sm)] text-[var(--color-text)]">{audienceText}</span>
        {share.cascade && <Badge variant="secondary">cascade</Badge>}
        {share.expired && <Badge variant="danger">expired</Badge>}
        {!share.expired && share.expires_at && (
          <Badge variant="outline">until {new Date(share.expires_at).toLocaleDateString()}</Badge>
        )}
      </div>
      <button
        type="button"
        onClick={onRevoke}
        disabled={revoking}
        aria-label="Revoke share"
        data-testid="share-revoke"
        className="text-[var(--color-text-muted)] hover:text-[var(--color-danger)]"
      >
        <Trash2 className="h-4 w-4" />
      </button>
    </div>
  );
}

function entityTypeLabel(t: ShareEntityType): string {
  switch (t) {
    case 'page':
      return 'page';
    case 'ticket':
      return 'ticket';
    case 'project_item':
      return 'item';
  }
}
