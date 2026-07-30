import { useState } from 'react';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import { FieldLabel, FieldHint } from '../ui/field';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '../ui/dialog';
import { SegmentedControl } from '../ui/segmented';
import { PersonTeamPicker, type PickedSubject } from '../PersonTeamPicker';
import {
  friendlyErrorMessage,
  useDeleteDashboard,
  useUpdateDashboard,
  type Dashboard,
  type ViewVisibility,
} from '../../lib/api';

interface DashboardSettingsDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  orgId: string;
  dashboard: Dashboard;
  onDeleted: () => void;
}

/**
 * Rename a dashboard, decide who can see it, make it the Home default, or
 * delete it.
 *
 * SHARING SHARES THE ARRANGEMENT. The copy says so, because the alternative
 * assumption — that sharing a dashboard shares what is on it — is the one
 * somebody will make, and it is wrong: every gadget re-resolves against
 * whoever opens it.
 *
 * The team picker is PersonTeamPicker, the shipped one. There must never be a
 * second picker (docs/design/shared-surfaces.md §1).
 */
export function DashboardSettingsDialog({
  open,
  onOpenChange,
  orgId,
  dashboard,
  onDeleted,
}: DashboardSettingsDialogProps) {
  const update = useUpdateDashboard(orgId);
  const remove = useDeleteDashboard(orgId);

  const [name, setName] = useState(dashboard.name);
  const [visibility, setVisibility] = useState<ViewVisibility>(dashboard.visibility);
  const [team, setTeam] = useState<PickedSubject | null>(
    dashboard.visibility_team_id
      ? {
          kind: 'team',
          id: dashboard.visibility_team_id,
          label: dashboard.team_name ?? 'a team',
        }
      : null,
  );
  const [isDefault, setIsDefault] = useState(dashboard.is_default);
  const [confirmingDelete, setConfirmingDelete] = useState(false);

  const teamMissing = visibility === 'team' && !team;
  const blocked = !name.trim() || teamMissing;

  async function save() {
    if (blocked) return;
    try {
      await update.mutateAsync({
        dashboardId: dashboard.id,
        req: {
          name: name.trim(),
          description: dashboard.description,
          module: dashboard.module,
          visibility,
          visibility_team_id: visibility === 'team' ? (team?.id ?? null) : null,
          is_default: isDefault,
        },
      });
      onOpenChange(false);
    } catch {
      // Rendered from the mutation's error state below.
    }
  }

  async function destroy() {
    try {
      await remove.mutateAsync(dashboard.id);
      onOpenChange(false);
      onDeleted();
    } catch {
      // Rendered from the mutation's error state below.
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="dashboard-settings-dialog">
        <DialogHeader>
          <DialogTitle>Dashboard settings</DialogTitle>
          <DialogDescription>
            Sharing a dashboard shares its arrangement. Every gadget still resolves against each
            reader&apos;s own access, so people see their own rows and their own numbers.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4 py-2">
          <div>
            <FieldLabel htmlFor="dashboard-rename">Name</FieldLabel>
            <Input
              id="dashboard-rename"
              data-testid="dashboard-rename"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>

          <div>
            <FieldLabel id="dashboard-visibility-label">Who can see it</FieldLabel>
            <SegmentedControl
              testId="dashboard-visibility-control"
              aria-label="Who can see this dashboard"
              value={visibility}
              onChange={(v) => setVisibility(v as ViewVisibility)}
              options={[
                { value: 'private', label: 'Only me' },
                { value: 'team', label: 'A team' },
                { value: 'org', label: 'Organisation' },
              ]}
            />
          </div>

          {visibility === 'team' && (
            <div>
              <FieldLabel>Team</FieldLabel>
              <PersonTeamPicker
                orgId={orgId}
                subjects="team"
                value={team}
                onChange={setTeam}
                placeholder="Choose a team you belong to"
                testId="dashboard-team-picker"
              />
              {teamMissing && (
                <FieldHint>A team-shared dashboard needs a team you belong to.</FieldHint>
              )}
            </div>
          )}

          {dashboard.module === 'home' && (
            <label className="flex items-start gap-2 text-[var(--text-sm)] text-[var(--color-text)]">
              <input
                type="checkbox"
                data-testid="dashboard-default"
                checked={isDefault}
                onChange={(e) => setIsDefault(e.target.checked)}
                className="mt-0.5"
              />
              <span>
                Show this on Home
                <span className="block text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  You have one Home dashboard at a time. Choosing this stands the current one down.
                </span>
              </span>
            </label>
          )}

          {(update.error || remove.error) && (
            <p data-testid="dashboard-settings-error" className="text-[var(--text-sm)] text-[var(--color-danger)]">
              {friendlyErrorMessage(
                update.error ?? remove.error,
                'That change was not saved. Try again.',
              )}
            </p>
          )}
        </div>

        <DialogFooter>
          {confirmingDelete ? (
            <>
              <span className="mr-auto text-[var(--text-sm)] text-[var(--color-text-muted)]">
                Delete “{dashboard.name}”? Its gadgets go with it.
              </span>
              <Button variant="outline" type="button" onClick={() => setConfirmingDelete(false)}>
                Keep it
              </Button>
              <Button
                variant="destructive"
                data-testid="dashboard-delete-confirm"
                onClick={destroy}
                disabled={remove.isPending}
              >
                Delete
              </Button>
            </>
          ) : (
            <>
              <Button
                variant="outline"
                type="button"
                className="mr-auto"
                data-testid="dashboard-delete"
                onClick={() => setConfirmingDelete(true)}
              >
                Delete
              </Button>
              <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button data-testid="dashboard-settings-save" onClick={save} disabled={blocked || update.isPending}>
                {update.isPending ? 'Saving…' : 'Save'}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
