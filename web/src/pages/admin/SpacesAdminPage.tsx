import { useState } from 'react';
import { Pencil, Trash2 } from 'lucide-react';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useDeleteSpace,
  useSpaceContentsSummary,
  useSpaces,
  useTeams,
  useTicketRefRequired,
  useUpdateSpace,
  type Space,
  type SpaceVisibility,
} from '../../lib/api';
import { ModuleChip } from '../../shell/ModuleChip';
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
import { Field, FieldLabel } from '../../components/ui/field';
import { RadioCardGroup } from '../../components/ui/radio-card';
import { TicketRefField } from '../../components/TicketRefField';

/**
 * SpacesAdminPage (P2.5 W8): org-wide space governance — rename,
 * description, owner team, visibility, and soft delete behind a
 * confirmation that names the space and counts what it contains.
 */
export function SpacesAdminPage() {
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';
  const spaces = useSpaces(orgId);
  const [editing, setEditing] = useState<Space | null>(null);
  const [deleting, setDeleting] = useState<Space | null>(null);

  if (spaces.isLoading) {
    return <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading spaces…</p>;
  }
  if (spaces.error) {
    return (
      <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
        {friendlyErrorMessage(spaces.error, 'The space list could not be loaded.')}
      </p>
    );
  }

  const rows = spaces.data ?? [];

  return (
    <div data-testid="admin-spaces">
      <Card>
        <CardContent className="p-0">
          <div className="grid grid-cols-[minmax(200px,2fr)_auto_1fr_1fr_auto] items-center gap-x-[var(--space-3)] border-b border-[var(--color-border)] px-[var(--space-4)] py-[var(--space-2)] text-[var(--text-xs)] font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
            <span>Space</span>
            <span>Module</span>
            <span>Owner team</span>
            <span>Visibility</span>
            <span aria-hidden="true" />
          </div>
          {rows.length === 0 && (
            <p className="px-[var(--space-4)] py-[var(--space-6)] text-[var(--text-sm)] text-[var(--color-text-muted)]">
              No spaces yet.
            </p>
          )}
          {rows.map((s) => (
            <SpaceRow key={s.id} orgId={orgId} space={s} onEdit={() => setEditing(s)} onDelete={() => setDeleting(s)} />
          ))}
        </CardContent>
      </Card>

      {editing && (
        <EditSpaceDialog orgId={orgId} space={editing} onClose={() => setEditing(null)} />
      )}
      {deleting && (
        <DeleteSpaceDialog orgId={orgId} space={deleting} onClose={() => setDeleting(null)} />
      )}
    </div>
  );
}

function SpaceRow({ orgId, space, onEdit, onDelete }: {
  orgId: string;
  space: Space;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const teams = useTeams(orgId);
  const ownerName = (teams.data ?? []).find((t) => t.id === space.owner_team_id)?.name ?? '—';

  return (
    <div
      data-testid={`admin-space-row-${space.slug}`}
      className="grid grid-cols-[minmax(200px,2fr)_auto_1fr_1fr_auto] items-center gap-x-[var(--space-3)] border-b border-[var(--color-border)] px-[var(--space-4)] py-[var(--space-3)] last:border-b-0 hover:bg-[var(--color-surface-hover)]"
    >
      <span className="min-w-0">
        <span className="block truncate text-[var(--text-sm)] font-medium text-[var(--color-text)]">{space.name}</span>
        {space.description ? (
          <span className="block truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">{space.description}</span>
        ) : null}
      </span>
      <ModuleChip module={space.type} />
      <span className="truncate text-[var(--text-sm)] text-[var(--color-text-muted)]">{ownerName}</span>
      <span>
        <Badge variant={space.visibility === 'org' ? 'secondary' : 'outline'}>
          {space.visibility ?? 'discoverable'}
        </Badge>
      </span>
      <span className="flex gap-[var(--space-1)]">
        <Button variant="ghost" size="icon" aria-label={`Edit ${space.name}`} data-testid={`admin-space-edit-${space.slug}`} onClick={onEdit}>
          <Pencil className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" aria-label={`Delete ${space.name}`} data-testid={`admin-space-delete-${space.slug}`} onClick={onDelete}>
          <Trash2 className="h-4 w-4 text-[var(--color-danger)]" />
        </Button>
      </span>
    </div>
  );
}

function EditSpaceDialog({ orgId, space, onClose }: { orgId: string; space: Space; onClose: () => void }) {
  const ticketRefRequired = useTicketRefRequired(orgId);
  const teams = useTeams(orgId);
  const update = useUpdateSpace(orgId, space.id);
  const [name, setName] = useState(space.name);
  const [description, setDescription] = useState(space.description ?? '');
  const [ownerTeamID, setOwnerTeamID] = useState(space.owner_team_id ?? '');
  const [visibility, setVisibility] = useState<SpaceVisibility>(space.visibility ?? 'discoverable');
  const [ticketRef, setTicketRef] = useState('');
  const [error, setError] = useState<string | null>(null);

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="admin-space-edit-dialog">
        <DialogHeader>
          <DialogTitle>Edit {space.name}</DialogTitle>
          <DialogDescription>Name, description, owner team, and visibility.</DialogDescription>
        </DialogHeader>
        <div>
          <Field>
            <FieldLabel htmlFor="admin-space-name">Name</FieldLabel>
            <Input id="admin-space-name" value={name} onChange={(e) => setName(e.target.value)} data-testid="admin-space-name" />
          </Field>
          <Field>
            <FieldLabel htmlFor="admin-space-description" optional>Description</FieldLabel>
            <Input id="admin-space-description" value={description} onChange={(e) => setDescription(e.target.value)} data-testid="admin-space-description" />
          </Field>
          <Field>
            <FieldLabel htmlFor="admin-space-owner-team">Owner team</FieldLabel>
            <select
              id="admin-space-owner-team"
              value={ownerTeamID}
              onChange={(e) => setOwnerTeamID(e.target.value)}
              data-testid="admin-space-owner-team"
              className={cn(
                'block h-9 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)]',
                'bg-[var(--color-input)] px-2 text-[var(--text-sm)] text-[var(--color-text)]',
                'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
              )}
            >
              {(teams.data ?? []).map((t) => (
                <option key={t.id} value={t.id}>{t.name}</option>
              ))}
            </select>
          </Field>
          <Field>
            {/* The one visibility edit surface: set_visibility is org-admin
                only, so the card lives here and not in space settings. */}
            <FieldLabel id="admin-space-visibility-label">Visibility</FieldLabel>
            <RadioCardGroup
              options={[
                {
                  value: 'hidden',
                  title: 'Hidden',
                  description: 'Invisible except to people with a grant. Does not appear in the directory.',
                },
                {
                  value: 'discoverable',
                  title: 'Discoverable',
                  description: 'Listed in the directory for everyone in the org, but only people with a grant can open it. Others see a locked row.',
                },
                {
                  value: 'org',
                  title: 'Org',
                  description: 'Everyone in the org can view this space as a viewer. Grants still control editing.',
                },
              ]}
              value={visibility}
              onChange={setVisibility}
              aria-label="Visibility"
              testId="admin-space-visibility"
            />
          </Field>
          <TicketRefField
            orgId={orgId}
            value={ticketRef}
            onChange={setTicketRef}
            required={ticketRefRequired}
            testId="admin-space-ticket-ref"
            hint="Recorded on the audit event for this change."
          />
          {error && <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button
            disabled={
              update.isPending || !name.trim() || (ticketRefRequired && !ticketRef.trim())
            }
            data-testid="admin-space-save"
            onClick={() => {
              setError(null);
              // PUT semantics: echo fields the dialog does not edit (icon,
              // is_private, key) so they survive the update.
              update.mutate(
                {
                  name: name.trim(),
                  key: space.key,
                  description: description || null,
                  icon: space.icon ?? null,
                  is_private: space.is_private,
                  owner_team_id: ownerTeamID || undefined,
                  visibility,
                  ticketRef: ticketRef.trim() || undefined,
                },
                {
                  onSuccess: onClose,
                  onError: (err) => setError(friendlyErrorMessage(err, 'The space could not be saved.')),
                },
              );
            }}
          >
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * DeleteSpaceDialog: soft delete behind a confirmation that names the space
 * and counts what it contains — nobody deletes "a space", they delete
 * "Support (14 tickets, 3 pages)".
 */
function DeleteSpaceDialog({ orgId, space, onClose }: { orgId: string; space: Space; onClose: () => void }) {
  const ticketRefRequired = useTicketRefRequired(orgId);
  const summary = useSpaceContentsSummary(orgId, space.id);
  const del = useDeleteSpace(orgId);
  const [ticketRef, setTicketRef] = useState('');
  const [error, setError] = useState<string | null>(null);

  const counts = summary.data;
  const contents = counts
    ? [
        counts.tickets > 0 ? `${counts.tickets} ticket${counts.tickets === 1 ? '' : 's'}` : null,
        counts.pages > 0 ? `${counts.pages} page${counts.pages === 1 ? '' : 's'}` : null,
        counts.items > 0 ? `${counts.items} project item${counts.items === 1 ? '' : 's'}` : null,
      ].filter(Boolean)
    : null;

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent data-testid="admin-space-delete-dialog">
        <DialogHeader>
          <DialogTitle>Delete “{space.name}”?</DialogTitle>
          <DialogDescription data-testid="admin-space-delete-summary">
            {summary.isLoading && 'Counting what it contains…'}
            {!summary.isLoading && contents && contents.length > 0 && (
              <>This space contains {contents.join(', ')}. Everything becomes unavailable when the space is deleted.</>
            )}
            {!summary.isLoading && contents && contents.length === 0 && 'This space is empty.'}
            {!summary.isLoading && !contents && 'Its contents could not be counted — it may still contain work.'}
          </DialogDescription>
        </DialogHeader>
        {/* Optional, and not a gate, unless the deployment sets
            AZIMUTHAL_TICKET_REF_REQUIRED — in which case the delete button
            waits for a reference, because the server refuses the request
            without one. With the flag off (the default) the button stays
            enabled whether or not a reference is given. */}
        <TicketRefField
          orgId={orgId}
          value={ticketRef}
          onChange={setTicketRef}
          required={ticketRefRequired}
          disabled={del.isPending}
          testId="admin-space-delete-ticket-ref"
          hint="Recorded on the audit event for this deletion."
        />
        {error && <p className="text-[var(--text-sm)] text-[var(--color-danger)]">{error}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button
            variant="destructive"
            disabled={del.isPending || (ticketRefRequired && !ticketRef.trim())}
            data-testid="admin-space-delete-confirm"
            onClick={() => {
              setError(null);
              del.mutate({ id: space.id, ticketRef: ticketRef.trim() || undefined }, {
                onSuccess: onClose,
                onError: (err) => setError(friendlyErrorMessage(err, 'The space could not be deleted.')),
              });
            }}
          >
            Delete {space.name}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
