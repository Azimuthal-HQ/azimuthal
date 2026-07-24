import { useState } from 'react';
import { useParams } from 'react-router-dom';
import { Settings, ShieldOff, Trash2, UserPlus } from 'lucide-react';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { SegmentedControl } from '../../components/ui/segmented';
import { PersonTeamPicker, type PickedSubject } from '../../components/PersonTeamPicker';
import { BoardConfigSection } from './BoardConfigSection';
import { MODULES } from '../../shell/modules';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useCreateGrant,
  useRevokeGrant,
  useSpace,
  useSpaceGrants,
  useUpdateGrant,
  type GrantRole,
} from '../../lib/api';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const GRANT_ROLES: GrantRole[] = ['viewer', 'contributor', 'agent', 'space_admin'];

const selectClass = cn(
  'h-8 rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-2 text-[var(--text-sm)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
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
  const [subjectKind, setSubjectKind] = useState<'user' | 'team'>('user');
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
                className="flex items-center gap-3 border-b border-[var(--color-border)] px-1 py-2.5 last:border-b-0"
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

            {/* Add grant — subject kind toggle, then search (interior prototype). */}
            <div className="border-t border-[var(--color-border)] pt-4">
              <p className="mb-2 flex items-center gap-2 text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                <UserPlus className="h-4 w-4 text-[var(--color-primary)]" />
                Add a grant
              </p>
              <div className="flex flex-wrap items-center gap-2">
                <SegmentedControl
                  options={[
                    { value: 'user', label: 'User' },
                    { value: 'team', label: 'Team' },
                  ]}
                  value={subjectKind}
                  onChange={(kind) => {
                    setSubjectKind(kind);
                    // A selection of the other kind must not survive the
                    // toggle — the picker is controlled and would otherwise
                    // submit a subject_type contradicting the visible toggle.
                    setSubject(null);
                  }}
                  aria-label="Subject type"
                  fullWidth={false}
                  testId="grant-subject-kind"
                />
                <PersonTeamPicker
                  orgId={orgId}
                  subjects={subjectKind}
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
// SpaceSettingsPage
// ---------------------------------------------------------------------------

/**
 * Per-space settings (P2): access grants. Visibility moved to the admin
 * panel's space management — it is an org-level concern (set_visibility,
 * org admin only), so space settings no longer offers it. Space rename and
 * delete stay deferred per the v0.3 spec.
 */
export function SpaceSettingsPage() {
  const { spaceId } = useParams<{ spaceId: string }>();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const spaceQuery = useSpace(spaceId ?? '');
  const moduleDef = spaceQuery.data ? MODULES[spaceQuery.data.type] : null;

  if (!spaceId) return null;

  return (
    <div className="space-y-4" data-testid="space-settings-page">
      <div className="flex items-center gap-3">
        <div
          className="flex h-9 w-9 items-center justify-center rounded-[9px]"
          style={
            moduleDef
              ? {
                  backgroundColor: `color-mix(in srgb, var(${moduleDef.hueVar}) 16%, transparent)`,
                  color: `var(${moduleDef.hueVar})`,
                }
              : undefined
          }
        >
          <Settings className="h-[19px] w-[19px]" />
        </div>
        <div>
          <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
            Space settings
          </h1>
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            {spaceQuery.data ? `Access for ${spaceQuery.data.name}.` : 'Access.'}
          </p>
        </div>
      </div>

      <GrantsSection orgId={orgId} spaceId={spaceId} />

      {/* Board customization is Vector-only: Beacon's kanban is untouched. */}
      {spaceQuery.data?.type === 'vector' && <BoardConfigSection spaceId={spaceId} />}
    </div>
  );
}
