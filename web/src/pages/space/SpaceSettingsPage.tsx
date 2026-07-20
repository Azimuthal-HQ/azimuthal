import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { Settings, ShieldOff, Trash2, UserPlus } from 'lucide-react';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { PersonTeamPicker, type PickedSubject } from '../../components/PersonTeamPicker';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useCreateGrant,
  useRevokeGrant,
  useSpace,
  useSpaceGrants,
  useUpdateGrant,
  useUpdateSpace,
  type GrantRole,
  type Space,
  type SpaceVisibility,
} from '../../lib/api';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const GRANT_ROLES: GrantRole[] = ['viewer', 'contributor', 'agent', 'space_admin'];

const VISIBILITY_OPTIONS: { value: SpaceVisibility; label: string; help: string }[] = [
  {
    value: 'hidden',
    label: 'Hidden',
    help: 'Invisible except to people with a grant. The space does not appear in the directory.',
  },
  {
    value: 'discoverable',
    label: 'Discoverable',
    help: 'Listed in the directory for everyone in the org, but only people with a grant can open it. Others see a locked row.',
  },
  {
    value: 'org',
    label: 'Org',
    help: 'Everyone in the org can view this space (implicit viewer). Grants still control editing.',
  },
];

const selectClass = cn(
  'h-8 rounded-[var(--radius-md)] border border-[var(--color-border)]',
  'bg-[var(--color-surface)] px-2 text-[var(--text-sm)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)]',
);

// ---------------------------------------------------------------------------
// Grants section
// ---------------------------------------------------------------------------

function GrantsSection({ orgId, spaceId }: { orgId: string; spaceId: string }) {
  const grantsQuery = useSpaceGrants(orgId, spaceId);
  const createGrant = useCreateGrant(orgId, spaceId);
  const updateGrant = useUpdateGrant(orgId, spaceId);
  const revokeGrant = useRevokeGrant(orgId, spaceId);

  const [subject, setSubject] = useState<PickedSubject | null>(null);
  const [newRole, setNewRole] = useState<GrantRole>('viewer');

  const forbidden = grantsQuery.error?.status === 403;

  function handleAdd() {
    if (!subject) return;
    createGrant.mutate(
      { subject_type: subject.kind, subject_id: subject.id, role: newRole },
      { onSuccess: () => setSubject(null) },
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Access grants</CardTitle>
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Who can enter this space, and with what role. Team grants reach every member of the team
          and its sub-teams.
        </p>
      </CardHeader>
      <CardContent className="space-y-4">
        {forbidden ? (
          <div
            data-testid="grants-forbidden"
            className="flex flex-col items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)] px-[var(--space-6)] py-[var(--space-8)] text-center"
          >
            <ShieldOff className="h-8 w-8 text-[var(--color-text-muted)]" />
            <p className="mt-[var(--space-3)] text-[var(--text-sm)] font-medium text-[var(--color-text)]">
              You need manage_grants on this space
            </p>
            <p className="mt-[var(--space-1)] max-w-md text-[var(--text-sm)] text-[var(--color-text-muted)]">
              Only space admins and org admins can view or change who has access here.
            </p>
          </div>
        ) : grantsQuery.isLoading ? (
          <p className="py-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading grants…</p>
        ) : grantsQuery.error ? (
          <p className="py-2 text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(grantsQuery.error, 'The grants for this space could not be loaded.')}
          </p>
        ) : (
          <>
            {(grantsQuery.data ?? []).length === 0 && (
              <p className="py-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
                No grants yet. Add one below.
              </p>
            )}
            {(grantsQuery.data ?? []).map((grant) => (
              <div
                key={grant.id}
                data-testid="grant-row"
                className="flex items-center gap-3 rounded-[var(--radius-md)] px-2 py-1.5 hover:bg-[var(--color-surface-hover)]"
              >
                <span className="min-w-0 flex-1 truncate text-[var(--text-sm)] text-[var(--color-text)]">
                  {grant.subject_name || grant.subject_id}
                  {grant.subject_missing && (
                    <span className="ml-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                      (no longer in this org)
                    </span>
                  )}
                </span>
                <Badge variant={grant.subject_type === 'team' ? 'secondary' : 'outline'}>
                  {grant.subject_type}
                </Badge>
                <select
                  value={grant.role}
                  data-testid="grant-role-select"
                  aria-label={`Role for ${grant.subject_name || grant.subject_id}`}
                  disabled={updateGrant.isPending}
                  onChange={(e) =>
                    updateGrant.mutate({ grantId: grant.id, role: e.target.value as GrantRole })
                  }
                  className={selectClass}
                >
                  {GRANT_ROLES.map((r) => (
                    <option key={r} value={r}>
                      {r}
                    </option>
                  ))}
                </select>
                <Button
                  variant="ghost"
                  size="icon"
                  data-testid="grant-revoke-button"
                  aria-label={`Revoke access for ${grant.subject_name || grant.subject_id}`}
                  disabled={revokeGrant.isPending}
                  onClick={() => revokeGrant.mutate(grant.id)}
                  className="h-8 w-8 text-[var(--color-text-muted)] hover:text-[var(--color-danger)]"
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
            ))}
            {(updateGrant.error || revokeGrant.error) && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
                {updateGrant.error
                  ? friendlyErrorMessage(updateGrant.error, 'The role could not be changed.')
                  : friendlyErrorMessage(revokeGrant.error, 'The grant could not be revoked.')}
              </p>
            )}

            {/* Add grant */}
            <div className="border-t border-[var(--color-border)] pt-4">
              <p className="mb-2 flex items-center gap-2 text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                <UserPlus className="h-4 w-4 text-[var(--color-primary)]" />
                Add a grant
              </p>
              <div className="flex flex-wrap items-center gap-2">
                <PersonTeamPicker
                  orgId={orgId}
                  subjects="both"
                  value={subject}
                  onChange={setSubject}
                  testId="grant-subject-picker"
                />
                <select
                  data-testid="grant-add-role-select"
                  value={newRole}
                  onChange={(e) => setNewRole(e.target.value as GrantRole)}
                  className={selectClass}
                  aria-label="Role for new grant"
                >
                  {GRANT_ROLES.map((r) => (
                    <option key={r} value={r}>
                      {r}
                    </option>
                  ))}
                </select>
                <Button
                  size="sm"
                  data-testid="grant-add-button"
                  disabled={createGrant.isPending || !subject}
                  onClick={handleAdd}
                >
                  {createGrant.isPending ? 'Adding…' : 'Add grant'}
                </Button>
              </div>
              {createGrant.error && (
                <p className="mt-2 text-[var(--text-sm)] text-[var(--color-danger)]">
                  {friendlyErrorMessage(createGrant.error, 'The grant could not be added.')}
                </p>
              )}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Visibility section
// ---------------------------------------------------------------------------

function VisibilitySection({
  orgId,
  spaceId,
  space,
}: {
  orgId: string;
  spaceId: string;
  space: Space | undefined;
}) {
  const updateSpace = useUpdateSpace(orgId, spaceId);
  const current = space?.visibility;

  function handleSelect(visibility: SpaceVisibility) {
    if (!space || visibility === current) return;
    // The backend PUT is a full update for name/description/icon/is_private —
    // echo the current values so a visibility change touches nothing else.
    updateSpace.mutate({
      name: space.name,
      description: space.description ?? null,
      icon: space.icon ?? null,
      is_private: space.is_private ?? false,
      visibility,
    });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Visibility</CardTitle>
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Controls how this space appears to org members without a grant.
        </p>
      </CardHeader>
      <CardContent>
        <div className="space-y-2" role="radiogroup" aria-label="Space visibility">
          {VISIBILITY_OPTIONS.map((opt) => {
            const active = current === opt.value;
            return (
              <button
                key={opt.value}
                type="button"
                role="radio"
                aria-checked={active}
                data-testid={`visibility-option-${opt.value}`}
                disabled={!space || updateSpace.isPending}
                onClick={() => handleSelect(opt.value)}
                className={cn(
                  'flex w-full flex-col items-start rounded-[var(--radius-lg)] border p-3 text-left transition-colors',
                  active
                    ? 'border-[var(--color-primary)] bg-[var(--color-primary-muted)]'
                    : 'border-[var(--color-border)] hover:border-[var(--color-text-muted)]',
                  (!space || updateSpace.isPending) && 'cursor-not-allowed opacity-70',
                )}
              >
                <span
                  className={cn(
                    'text-[var(--text-sm)] font-medium',
                    active ? 'text-[var(--color-primary)]' : 'text-[var(--color-text)]',
                  )}
                >
                  {opt.label}
                </span>
                <span className="mt-0.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  {opt.help}
                </span>
              </button>
            );
          })}
        </div>
        {updateSpace.error && (
          <p className="mt-3 text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(updateSpace.error, 'The visibility change could not be saved.')}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// SpaceSettingsPage
// ---------------------------------------------------------------------------

/**
 * Per-space settings (P2): access grants and visibility. Space rename and
 * delete stay deferred per the v0.3 spec.
 */
export function SpaceSettingsPage() {
  const { spaceId } = useParams<{ spaceId: string }>();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const spaceQuery = useSpace(spaceId ?? '');

  if (!spaceId) return null;

  return (
    <div className="space-y-6" data-testid="space-settings-page">
      <div className="flex items-center gap-3">
        <Settings className="h-6 w-6 text-[var(--color-primary)]" />
        <div>
          <h1 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
            Space settings
          </h1>
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            {spaceQuery.data ? `Access and visibility for ${spaceQuery.data.name}.` : 'Access and visibility.'}
          </p>
        </div>
      </div>

      <GrantsSection orgId={orgId} spaceId={spaceId} />
      <VisibilitySection orgId={orgId} spaceId={spaceId} space={spaceQuery.data} />
    </div>
  );
}
