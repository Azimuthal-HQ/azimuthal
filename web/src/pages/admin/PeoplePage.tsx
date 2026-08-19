import { useMemo, useRef, useState, type ChangeEvent } from 'react';
import { Check as CheckIcon, Copy, Mail, MoreHorizontal, Pencil, UserPlus } from 'lucide-react';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useAdminCreateUserWithLink,
  useAdminIssueResetLink,
  useCreateInvites,
  useInvites,
  useOrgPeople,
  usePersonLifecycle,
  useRemovePerson,
  useResendInvite,
  useUploadUserAvatar,
  useRevokeInvite,
  useTeams,
  useTicketRefRequired,
  useUpdatePerson,
  type CreatedInvite,
  type CredentialLinkResult,
  type Invite,
  type InviteOutcome,
  type Person,
} from '../../lib/api';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { Card, CardContent } from '../../components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../../components/ui/dialog';
import { Input } from '../../components/ui/input';
import { TicketRefField } from '../../components/TicketRefField';

type StatusFilter = 'all' | 'active' | 'invited' | 'deactivated';

/**
 * PeoplePage (P2.5 W4): every member of the org — person, org role, primary
 * team, status, last sign-in — plus pending invites, searchable and
 * filterable, with the full lifecycle on each row. Deactivation always
 * terminates sessions; there is deliberately no option to keep them
 * signed in. Last-admin protection errors surface verbatim — the backend
 * writes them for humans.
 */
export function PeoplePage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const peopleQuery = useOrgPeople(orgId);
  const invitesQuery = useInvites(orgId);

  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [inviteOpen, setInviteOpen] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);

  const q = search.trim().toLowerCase();
  const people = useMemo(() => {
    const all = peopleQuery.data ?? [];
    return all.filter((p) => {
      if (q && !p.display_name.toLowerCase().includes(q) && !p.email.toLowerCase().includes(q)) {
        return false;
      }
      if (statusFilter === 'active') return p.status === 'active';
      if (statusFilter === 'deactivated') return p.status === 'deactivated';
      if (statusFilter === 'invited') return false;
      return true;
    });
  }, [peopleQuery.data, q, statusFilter]);

  const invites = useMemo(() => {
    const all = invitesQuery.data ?? [];
    if (statusFilter === 'active' || statusFilter === 'deactivated') return [];
    return all.filter((i) => !q || i.email.toLowerCase().includes(q));
  }, [invitesQuery.data, q, statusFilter]);

  if (peopleQuery.isLoading) {
    return <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading people…</p>;
  }
  if (peopleQuery.error) {
    return (
      <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
        {friendlyErrorMessage(peopleQuery.error, 'The member list could not be loaded.')}
      </p>
    );
  }

  return (
    <div data-testid="admin-people">
      <div className="mb-[var(--space-4)] flex flex-wrap items-center gap-[var(--space-2)]">
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search by name or email…"
          data-testid="people-search"
          className="h-9 w-72"
        />
        <select
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
          data-testid="people-status-filter"
          className={cn(
            'h-9 rounded-[var(--radius-lg)] border border-[var(--color-border)]',
            'bg-[var(--color-input)] px-2 text-[var(--text-sm)] text-[var(--color-text)]',
            'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
          )}
        >
          <option value="all">All statuses</option>
          <option value="active">Active</option>
          <option value="invited">Invited</option>
          <option value="deactivated">Deactivated</option>
        </select>
        <div className="ml-auto flex gap-[var(--space-2)]">
          <Button size="sm" variant="outline" onClick={() => setCreateOpen(true)} data-testid="people-create-user-button">
            <UserPlus className="mr-1.5 h-4 w-4" />
            Create user
          </Button>
          <Button size="sm" onClick={() => setInviteOpen(true)} data-testid="people-invite-button">
            <Mail className="mr-1.5 h-4 w-4" />
            Invite people
          </Button>
        </div>
      </div>

      {invites.length > 0 && (
        <PendingInvites orgId={orgId} invites={invites} />
      )}

      <Card>
        <CardContent className="p-0">
          <div className="grid grid-cols-[minmax(220px,2fr)_1fr_1fr_1fr_1fr_auto] items-center gap-x-[var(--space-3)] border-b border-[var(--color-border)] px-[var(--space-4)] py-[var(--space-2)] text-[var(--text-xs)] font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
          <span>Person</span>
          <span>Org role</span>
          <span>Primary team</span>
          <span>Status</span>
          <span>Last sign-in</span>
          <span aria-hidden="true" />
          </div>
          {people.length === 0 && (
            <p className="px-[var(--space-4)] py-[var(--space-6)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
              No members match.
            </p>
          )}
          {people.map((p) => (
            <PersonRow key={p.user_id} orgId={orgId} person={p} isSelf={p.user_id === user?.id} />
          ))}
        </CardContent>
      </Card>

      <InviteDialog orgId={orgId} open={inviteOpen} onClose={() => setInviteOpen(false)} />
      <CreateUserDialog orgId={orgId} open={createOpen} onClose={() => setCreateOpen(false)} />
    </div>
  );
}

/**
 * LinkOnceNote shows a one-time credential link with a copy button — the
 * credential-link analogue of InviteLinkNote, reused for the created sign-in
 * link and the generated reset link.
 */
function LinkOnceNote({ url, delivered, note, testid }: {
  url: string;
  delivered?: boolean;
  note?: string;
  testid: string;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="mt-2 rounded-[var(--radius-md)] bg-[var(--color-surface-hover)] p-[var(--space-2)]">
      {note && <p className="mb-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">{note}</p>}
      <div className="flex items-center gap-[var(--space-2)]">
        <code data-testid={testid} className="min-w-0 flex-1 truncate font-mono text-[var(--text-xs)] text-[var(--color-text)]" title={url}>
          {url}
        </code>
        <Button
          variant="outline"
          size="sm"
          data-testid={`${testid}-copy`}
          onClick={() => {
            void navigator.clipboard?.writeText(url);
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          }}
        >
          <Copy className="mr-1 h-3.5 w-3.5" />
          {copied ? 'Copied' : 'Copy link'}
        </Button>
      </div>
      <p className="mt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
        {delivered
          ? 'Also sent by email.'
          : 'Shown once — copy it now and hand it over. It works once and expires.'}
      </p>
    </div>
  );
}

/**
 * CreateUserDialog: the admin creates an account with a default grant and no
 * password, and gets a one-time sign-in link to hand over. The user sets their
 * own password when they open it — the maintainer's ruling for admin-created
 * accounts.
 */
function CreateUserDialog({ orgId, open, onClose }: { orgId: string; open: boolean; onClose: () => void }) {
  const create = useAdminCreateUserWithLink(orgId);
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [role, setRole] = useState('member');
  const [result, setResult] = useState<CredentialLinkResult | null>(null);

  const reset = () => {
    setEmail('');
    setName('');
    setRole('member');
    setResult(null);
    create.reset();
  };

  const submit = () => {
    create.mutate(
      { email: email.trim(), name: name.trim(), role },
      { onSuccess: (res) => setResult(res) },
    );
  };

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) { reset(); onClose(); } }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create a user</DialogTitle>
          <DialogDescription>
            Creates an account with no password and gives you a one-time sign-in link to hand over.
            They choose their own password when they open it.
          </DialogDescription>
        </DialogHeader>

        {result ? (
          <div data-testid="create-user-result">
            <LinkOnceNote
              url={result.url ?? ''}
              delivered={result.delivered}
              note="Sign-in link for the new account:"
              testid="create-user-link"
            />
          </div>
        ) : (
          <div className="space-y-[var(--space-3)]">
            <label className="block text-[var(--text-sm)] text-[var(--color-text)]">
              Email
              <Input value={email} onChange={(e) => setEmail(e.target.value)} type="email" placeholder="you@example.com" data-testid="create-user-email" className="mt-1" />
            </label>
            <label className="block text-[var(--text-sm)] text-[var(--color-text)]">
              Name
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Ada Lovelace" data-testid="create-user-name" className="mt-1" />
            </label>
            <label className="block text-[var(--text-sm)] text-[var(--color-text)]">
              Org role
              <select
                value={role}
                onChange={(e) => setRole(e.target.value)}
                data-testid="create-user-role"
                className={cn(
                  'mt-1 h-9 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
                  'bg-[var(--color-input)] px-2 text-[var(--text-sm)] text-[var(--color-text)]',
                )}
              >
                <option value="member">Member</option>
                <option value="admin">Admin</option>
                <option value="owner">Owner</option>
              </select>
            </label>
            {create.error && (
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]" data-testid="create-user-error">
                {friendlyErrorMessage(create.error, 'The user could not be created.')}
              </p>
            )}
          </div>
        )}

        <DialogFooter>
          {result ? (
            <Button data-testid="create-user-done" onClick={() => { reset(); onClose(); }}>Done</Button>
          ) : (
            <>
              <Button variant="outline" onClick={() => { reset(); onClose(); }}>Cancel</Button>
              <Button
                onClick={submit}
                disabled={create.isPending || !email.trim() || !name.trim()}
                data-testid="create-user-submit"
              >
                {create.isPending ? 'Creating…' : 'Create and get link'}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PersonRow({ orgId, person, isSelf }: { orgId: string; person: Person; isSelf: boolean }) {
  const ticketRefRequired = useTicketRefRequired(orgId);
  const [menuOpen, setMenuOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<'deactivate' | 'remove' | null>(null);
  // The operator reference for the two audited destructive actions. Cleared
  // whenever the confirmation opens so one row's reference never leaks into
  // the next action. Optional unless the deployment requires one.
  const [ticketRef, setTicketRef] = useState('');
  const lifecycle = usePersonLifecycle(orgId);
  const update = useUpdatePerson(orgId);
  const remove = useRemovePerson(orgId);
  const teams = useTeams(orgId);
  const uploadAvatar = useUploadUserAvatar(orgId);
  const issueReset = useAdminIssueResetLink(orgId);
  const [resetLink, setResetLink] = useState<CredentialLinkResult | null>(null);

  const [editingName, setEditingName] = useState(false);
  const [nameDraft, setNameDraft] = useState(person.display_name);
  const fileRef = useRef<HTMLInputElement>(null);

  const busy = lifecycle.isPending || update.isPending || remove.isPending || uploadAvatar.isPending;

  const saveName = () => {
    const name = nameDraft.trim();
    if (!name || name === person.display_name) {
      setEditingName(false);
      return;
    }
    setError(null);
    update.mutate(
      { userId: person.user_id, display_name: name },
      {
        onSuccess: () => setEditingName(false),
        onError: (err) => setError(friendlyErrorMessage(err, 'The display name could not be changed.')),
      },
    );
  };

  const onPickAvatar = (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setError(null);
    uploadAvatar.mutate(
      { userId: person.user_id, file },
      { onError: (err) => setError(friendlyErrorMessage(err, 'The avatar could not be uploaded.')) },
    );
  };

  const runLifecycle = (action: 'deactivate' | 'reactivate' | 'force-logout', ref?: string) => {
    setError(null);
    lifecycle.mutate(
      { userId: person.user_id, action, ticketRef: ref?.trim() || undefined },
      {
        onError: (err) =>
          setError(friendlyErrorMessage(err, `The account could not be ${action === 'force-logout' ? 'signed out' : `${action}d`}.`)),
        onSettled: () => setConfirming(null),
      },
    );
  };

  return (
    <div
      data-testid={`person-row-${person.email}`}
      className="border-b border-[var(--color-border)] px-[var(--space-4)] py-[var(--space-3)] last:border-b-0 hover:bg-[var(--color-surface-hover)]"
    >
      <div className="grid grid-cols-[minmax(220px,2fr)_1fr_1fr_1fr_1fr_auto] items-center gap-x-[var(--space-3)]">
        <span className="flex min-w-0 items-center gap-[var(--space-2)]">
          <button
            type="button"
            data-testid={`person-avatar-upload-${person.email}`}
            onClick={() => fileRef.current?.click()}
            disabled={busy}
            title="Change avatar"
            className="h-8 w-8 shrink-0 overflow-hidden rounded-full"
          >
            {person.avatar_url ? (
              <img src={person.avatar_url} alt="" className="h-8 w-8 rounded-full object-cover" />
            ) : (
              <span className="flex h-8 w-8 items-center justify-center rounded-full bg-[var(--color-primary-muted)] text-[var(--text-sm)] font-medium text-[var(--color-primary)]">
                {person.display_name.charAt(0).toUpperCase()}
              </span>
            )}
          </button>
          <input
            ref={fileRef}
            type="file"
            accept="image/png,image/jpeg,image/webp,image/gif"
            className="hidden"
            data-testid={`person-avatar-input-${person.email}`}
            onChange={onPickAvatar}
          />
          <span className="min-w-0">
            {editingName ? (
              <span className="flex items-center gap-1">
                <input
                  autoFocus
                  data-testid={`person-name-input-${person.email}`}
                  value={nameDraft}
                  disabled={busy}
                  onChange={(e) => setNameDraft(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') saveName();
                    if (e.key === 'Escape') setEditingName(false);
                  }}
                  className="h-7 min-w-0 flex-1 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-2 text-[var(--text-sm)] text-[var(--color-text)] focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]"
                />
                <button
                  type="button"
                  data-testid={`person-name-save-${person.email}`}
                  onClick={saveName}
                  disabled={busy}
                  title="Save name"
                  className="text-[var(--color-primary)] hover:text-[var(--color-primary-hover)]"
                >
                  <CheckIcon className="h-4 w-4" />
                </button>
              </span>
            ) : (
              <span className="flex items-center gap-1 text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                <span className="truncate">{person.display_name}</span>
                {isSelf && <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">(you)</span>}
                <button
                  type="button"
                  data-testid={`person-name-edit-${person.email}`}
                  onClick={() => {
                    setNameDraft(person.display_name);
                    setEditingName(true);
                  }}
                  title="Edit display name"
                  className="shrink-0 text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
                >
                  <Pencil className="h-3 w-3" />
                </button>
              </span>
            )}
            <span className="block truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">{person.email}</span>
          </span>
        </span>

        <span>
          {person.org_role === 'owner' ? (
            <Badge variant="secondary" data-testid="person-role-badge">owner</Badge>
          ) : (
            <select
              value={person.org_role}
              disabled={busy}
              data-testid={`person-role-select-${person.email}`}
              onChange={(e) => {
                setError(null);
                update.mutate(
                  { userId: person.user_id, org_role: e.target.value },
                  { onError: (err) => setError(friendlyErrorMessage(err, 'The role could not be changed.')) },
                );
              }}
              className="h-7 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-1 text-[var(--text-sm)] text-[var(--color-text)] focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]"
            >
              <option value="member">member</option>
              <option value="admin">admin</option>
            </select>
          )}
        </span>

        <span>
          <select
            value={person.primary_team_id ?? ''}
            disabled={busy || teams.isLoading}
            data-testid={`person-team-select-${person.email}`}
            onChange={(e) => {
              if (!e.target.value) return;
              setError(null);
              update.mutate(
                { userId: person.user_id, primary_team_id: e.target.value },
                { onError: (err) => setError(friendlyErrorMessage(err, 'The primary team could not be changed.')) },
              );
            }}
            className="h-7 max-w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-1 text-[var(--text-sm)] text-[var(--color-text)] focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]"
          >
            {!person.primary_team_id && <option value="">—</option>}
            {(teams.data ?? []).map((t) => (
              <option key={t.id} value={t.id}>{t.name}</option>
            ))}
          </select>
        </span>

        <span>
          {person.status === 'active' ? (
            <Badge variant="success" data-testid={`person-status-${person.email}`}>active</Badge>
          ) : (
            <Badge variant="danger" data-testid={`person-status-${person.email}`}>deactivated</Badge>
          )}
        </span>

        <span className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          {person.last_login_at ? new Date(person.last_login_at).toLocaleString() : 'Never'}
        </span>

        <span className="relative">
          <Button
            variant="ghost"
            size="icon"
            aria-label={`Actions for ${person.display_name}`}
            data-testid={`person-actions-${person.email}`}
            onClick={() => setMenuOpen((v) => !v)}
          >
            <MoreHorizontal className="h-4 w-4" />
          </Button>
          {menuOpen && (
            <span
              className="absolute right-0 top-full z-20 mt-1 w-56 rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface)] py-1 shadow-[var(--shadow-lg)]"
              onMouseLeave={() => setMenuOpen(false)}
            >
              <RowAction
                label="Sign out everywhere"
                testid={`person-force-logout-${person.email}`}
                onClick={() => { setMenuOpen(false); runLifecycle('force-logout'); }}
              />
              {person.status === 'active' && (
                <RowAction
                  label="Generate reset link"
                  testid={`person-reset-link-${person.email}`}
                  onClick={() => {
                    setMenuOpen(false);
                    setError(null);
                    setResetLink(null);
                    issueReset.mutate(person.email, {
                      onSuccess: (res) => setResetLink(res),
                      onError: (err) => setError(friendlyErrorMessage(err, 'A reset link could not be generated.')),
                    });
                  }}
                />
              )}
              {person.status === 'active' ? (
                <RowAction
                  label="Deactivate…"
                  destructive
                  testid={`person-deactivate-${person.email}`}
                  onClick={() => { setMenuOpen(false); setTicketRef(''); setConfirming('deactivate'); }}
                />
              ) : (
                <RowAction
                  label="Reactivate"
                  testid={`person-reactivate-${person.email}`}
                  onClick={() => { setMenuOpen(false); runLifecycle('reactivate'); }}
                />
              )}
              <RowAction
                label="Remove from organization…"
                destructive
                testid={`person-remove-${person.email}`}
                onClick={() => { setMenuOpen(false); setTicketRef(''); setConfirming('remove'); }}
              />
            </span>
          )}
        </span>
      </div>

      {error && (
        <p data-testid={`person-error-${person.email}`} className="mt-1 text-[var(--text-sm)] text-[var(--color-danger)]">
          {error}
        </p>
      )}

      {resetLink?.url && (
        <LinkOnceNote
          url={resetLink.url}
          delivered={resetLink.delivered}
          note={`Password-reset link for ${person.email}:`}
          testid={`person-reset-link-note-${person.email}`}
        />
      )}

      <Dialog open={confirming !== null} onOpenChange={(open) => { if (!open) setConfirming(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirming === 'deactivate'
                ? `Deactivate ${person.display_name}?`
                : `Remove ${person.display_name} from the organization?`}
            </DialogTitle>
            <DialogDescription>
              {confirming === 'deactivate'
                ? 'They will be signed out everywhere immediately and unable to sign in until reactivated. There is no way to deactivate an account while keeping its sessions alive.'
                : 'Their membership, team memberships, and space access are removed. Their account and everything they authored survive, with attribution intact.'}
            </DialogDescription>
          </DialogHeader>
          {/* Optional, and not a gate, unless the deployment sets
              AZIMUTHAL_TICKET_REF_REQUIRED — in which case the confirmation
              button waits for a reference, because the server would refuse the
              request without one. With the flag off (the default) the two-step
              confirmation keeps exactly the semantics it shipped with. */}
          <TicketRefField
            orgId={orgId}
            value={ticketRef}
            onChange={setTicketRef}
            required={ticketRefRequired}
            disabled={busy}
            testId="person-ticket-ref"
            hint="Recorded on the audit event for this change."
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirming(null)}>Cancel</Button>
            <Button
              variant="destructive"
              disabled={busy || (ticketRefRequired && !ticketRef.trim())}
              data-testid="person-confirm-action"
              onClick={() => {
                if (confirming === 'deactivate') {
                  runLifecycle('deactivate', ticketRef);
                } else {
                  setError(null);
                  remove.mutate({ id: person.user_id, ticketRef: ticketRef.trim() || undefined }, {
                    onError: (err) => setError(friendlyErrorMessage(err, 'The member could not be removed.')),
                    onSettled: () => setConfirming(null),
                  });
                }
              }}
            >
              {confirming === 'deactivate' ? 'Deactivate and sign out' : 'Remove from organization'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function RowAction({ label, onClick, destructive, testid }: {
  label: string;
  onClick: () => void;
  destructive?: boolean;
  testid: string;
}) {
  return (
    <button
      type="button"
      data-testid={testid}
      onClick={onClick}
      className={cn(
        'block w-full px-[var(--space-3)] py-[var(--space-2)] text-left text-[var(--text-sm)] hover:bg-[var(--color-surface-hover)]',
        destructive ? 'text-[var(--color-danger)]' : 'text-[var(--color-text)]',
      )}
    >
      {label}
    </button>
  );
}

function PendingInvites({ orgId, invites }: { orgId: string; invites: Invite[] }) {
  const revoke = useRevokeInvite(orgId);
  const resend = useResendInvite(orgId);
  const [freshLink, setFreshLink] = useState<CreatedInvite | null>(null);
  const [error, setError] = useState<string | null>(null);

  return (
    <Card className="mb-[var(--space-4)]" data-testid="pending-invites">
      <CardContent className="p-[var(--space-4)]">
        <h2 className="mb-[var(--space-2)] text-[var(--text-sm)] font-medium text-[var(--color-text)]">
          Pending invites
        </h2>
        {invites.map((inv) => (
          <div
            key={inv.id}
            data-testid={`invite-row-${inv.email}`}
            className="border-b border-[var(--color-border)] py-[var(--space-2)] last:border-b-0"
          >
           <div className="flex items-center gap-[var(--space-3)]">
            <Mail className="h-4 w-4 shrink-0 text-[var(--color-text-muted)]" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[var(--text-sm)] text-[var(--color-text)]">{inv.email}</span>
              <span className="block text-[var(--text-xs)] text-[var(--color-text-muted)]">
                {inv.org_role}
                {inv.team_name ? ` · ${inv.team_name}` : ''}
                {' · '}
                {inv.expired ? 'expired' : `expires ${new Date(inv.expires_at).toLocaleDateString()}`}
                {inv.invited_by_name ? ` · invited by ${inv.invited_by_name}` : ''}
              </span>
            </span>
            <Badge variant={inv.expired ? 'warning' : 'secondary'}>{inv.expired ? 'expired' : 'invited'}</Badge>
            <Button
              variant="outline"
              size="sm"
              data-testid={`invite-resend-${inv.email}`}
              disabled={resend.isPending}
              onClick={() => {
                setError(null);
                resend.mutate(inv.id, {
                  onSuccess: (created) => setFreshLink(created),
                  onError: (err) => setError(friendlyErrorMessage(err, 'The invite could not be resent.')),
                });
              }}
            >
              Resend
            </Button>
            <Button
              variant="ghost"
              size="sm"
              data-testid={`invite-revoke-${inv.email}`}
              disabled={revoke.isPending}
              onClick={() => {
                setError(null);
                revoke.mutate(inv.id, {
                  onError: (err) => setError(friendlyErrorMessage(err, 'The invite could not be revoked.')),
                });
              }}
            >
              Revoke
            </Button>
           </div>
            {freshLink && freshLink.id === inv.id && (
              <InviteLinkNote
                invite={freshLink}
                note="New link generated — the previous one no longer works. Copy it now and send it."
                onDismiss={() => setFreshLink(null)}
              />
            )}
          </div>
        ))}
        {error && <p className="mt-2 text-[var(--text-sm)] text-[var(--color-danger)]">{error}</p>}
      </CardContent>
    </Card>
  );
}

function InviteDialog({ orgId, open, onClose }: { orgId: string; open: boolean; onClose: () => void }) {
  const ticketRefRequired = useTicketRefRequired(orgId);
  const teams = useTeams(orgId);
  const createInvites = useCreateInvites(orgId);
  const [emailsRaw, setEmailsRaw] = useState('');
  const [orgRole, setOrgRole] = useState('member');
  const [teamId, setTeamId] = useState('');
  const [ticketRef, setTicketRef] = useState('');
  const [outcomes, setOutcomes] = useState<InviteOutcome[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const emails = emailsRaw
    .split(/[\s,;]+/)
    .map((e) => e.trim())
    .filter(Boolean);

  const reset = () => {
    setEmailsRaw('');
    setOrgRole('member');
    setTeamId('');
    setTicketRef('');
    setOutcomes(null);
    setError(null);
  };

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) { reset(); onClose(); } }}>
      <DialogContent data-testid="invite-dialog">
        <DialogHeader>
          <DialogTitle>Invite people</DialogTitle>
          <DialogDescription>
            One email per line, or separated by commas. Each person receives their own single-use link.
          </DialogDescription>
        </DialogHeader>

        {!outcomes && (
          <div className="space-y-[var(--space-3)]">
            <textarea
              value={emailsRaw}
              onChange={(e) => setEmailsRaw(e.target.value)}
              placeholder={'ada@example.com\ngrace@example.com'}
              rows={4}
              data-testid="invite-emails"
              className={cn(
                'w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
                'bg-[var(--color-input)] p-2 text-[var(--text-sm)] text-[var(--color-text)]',
                'placeholder:text-[var(--color-text-muted)] focus:outline-none focus:border-[var(--color-primary)] focus:ring-1 focus:ring-[var(--color-primary)]',
              )}
            />
            <div className="flex gap-[var(--space-3)]">
              <label className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-text)]">
                Org role
                <select
                  value={orgRole}
                  onChange={(e) => setOrgRole(e.target.value)}
                  data-testid="invite-org-role"
                  className="h-8 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-1 text-[var(--text-sm)] focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]"
                >
                  <option value="member">member</option>
                  <option value="admin">admin</option>
                </select>
              </label>
              <label className="flex items-center gap-2 text-[var(--text-sm)] text-[var(--color-text)]">
                Initial team
                <select
                  value={teamId}
                  onChange={(e) => setTeamId(e.target.value)}
                  data-testid="invite-team"
                  className="h-8 max-w-48 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-1 text-[var(--text-sm)] focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]"
                >
                  <option value="">Default team</option>
                  {(teams.data ?? []).filter((t) => !t.is_default).map((t) => (
                    <option key={t.id} value={t.id}>{t.name}</option>
                  ))}
                </select>
              </label>
            </div>
            <TicketRefField
              orgId={orgId}
              value={ticketRef}
              onChange={setTicketRef}
              required={ticketRefRequired}
              testId="invite-ticket-ref"
              hint="Recorded on the audit event for every invite in this batch."
            />
            {error && <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{error}</p>}
          </div>
        )}

        {outcomes && (
          <div className="space-y-[var(--space-2)]" data-testid="invite-outcomes">
            {outcomes.map((o) => (
              <div key={o.email} className="rounded-[var(--radius-md)] border border-[var(--color-border)] p-[var(--space-2)]">
                <div className="flex items-center gap-[var(--space-2)]">
                  <span className="min-w-0 flex-1 truncate text-[var(--text-sm)] text-[var(--color-text)]">{o.email}</span>
                  {o.status === 'created' ? (
                    <Badge variant="success">invited</Badge>
                  ) : (
                    <Badge variant="warning">{o.status.replaceAll('_', ' ')}</Badge>
                  )}
                </div>
                {o.status === 'created' && o.invite && (
                  <InviteLinkNote invite={o.invite} />
                )}
                {o.error && o.status !== 'created' && (
                  <p className="mt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">{o.error}</p>
                )}
              </div>
            ))}
          </div>
        )}

        <DialogFooter>
          {!outcomes ? (
            <>
              <Button variant="outline" onClick={() => { reset(); onClose(); }}>Cancel</Button>
              <Button
                disabled={
                  emails.length === 0 ||
                  createInvites.isPending ||
                  (ticketRefRequired && !ticketRef.trim())
                }
                data-testid="invite-submit"
                onClick={() => {
                  setError(null);
                  createInvites.mutate(
                    { emails, org_role: orgRole, team_id: teamId || null, ticketRef: ticketRef.trim() || undefined },
                    {
                      onSuccess: (res) => setOutcomes(res),
                      onError: (err) => setError(friendlyErrorMessage(err, 'The invites could not be created.')),
                    },
                  );
                }}
              >
                {emails.length > 1 ? `Invite ${emails.length} people` : 'Invite'}
              </Button>
            </>
          ) : (
            <Button data-testid="invite-done" onClick={() => { reset(); onClose(); }}>Done</Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * InviteLinkNote shows the one-time invite URL with a copy button. The raw
 * token exists only in this response — it is never stored and cannot be
 * retrieved again (resending generates a new link).
 */
function InviteLinkNote({ invite, note, onDismiss }: {
  invite: CreatedInvite;
  note?: string;
  onDismiss?: () => void;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="mt-2 rounded-[var(--radius-md)] bg-[var(--color-surface-hover)] p-[var(--space-2)]">
      {note && <p className="mb-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">{note}</p>}
      <div className="flex items-center gap-[var(--space-2)]">
        <code
          data-testid={`invite-link-${invite.email}`}
          className="min-w-0 flex-1 truncate font-mono text-[var(--text-xs)] text-[var(--color-text)]"
          title={invite.invite_url}
        >
          {invite.invite_url}
        </code>
        <Button
          variant="outline"
          size="sm"
          data-testid={`invite-copy-${invite.email}`}
          onClick={() => {
            void navigator.clipboard?.writeText(invite.invite_url);
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          }}
        >
          <Copy className="mr-1 h-3.5 w-3.5" />
          {copied ? 'Copied' : 'Copy link'}
        </Button>
        {onDismiss && (
          <Button variant="ghost" size="sm" onClick={onDismiss}>Dismiss</Button>
        )}
      </div>
      <p className="mt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
        {invite.delivered
          ? 'Also sent by email.'
          : 'Shown once — copy it now and send it to them. Resending generates a new link.'}
      </p>
    </div>
  );
}
