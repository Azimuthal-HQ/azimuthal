import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { AlertCircle, ChevronLeft, LayoutGrid, Plus, Settings2 } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Badge } from '../../components/ui/badge';
import { DashboardGrid } from '../../components/dashboards/DashboardGrid';
import { GadgetPicker } from '../../components/dashboards/GadgetPicker';
import { GadgetConfigDialog } from '../../components/dashboards/GadgetConfigDialog';
import { DashboardSettingsDialog } from '../../components/dashboards/DashboardSettingsDialog';
import { EmptyState } from '../../shell/EmptyState';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useDashboard,
  useSaveDashboardGadgets,
  type DashboardDetail,
  type GadgetRequest,
} from '../../lib/api';
import { getGadget, type GadgetDefinition } from '../../lib/dashboards/registry';

/** The stored gadgets, as the layout write wants them. */
function toRequests(detail: DashboardDetail): GadgetRequest[] {
  return detail.gadgets.map((g) => ({
    gadget_key: g.gadget_key,
    col_span: g.col_span,
    saved_view_id: g.saved_view_id,
    config: g.config,
  }));
}

/**
 * One dashboard.
 *
 * EVERY EDIT SAVES THE WHOLE COLLECTION. Adding, removing and reconfiguring a
 * gadget all rewrite the layout in one request, because the endpoint takes a
 * collection and spec §6 says so — "to avoid partial states". There is no
 * dirty-buffer and no save button: a person who removes a tile and closes the
 * tab has removed it, which is what the prototype's hover-to-remove implies.
 *
 * NO DRAG-AND-DROP, and the prototype does not show any: its gadgets are added
 * at the end and removed in place, with no drag affordance and no reorder
 * handler. Order is changed by removing and re-adding. Recorded as a
 * deviation-that-is-not-one in the phase report, with the board dnd pattern
 * noted as the thing to copy if it is ever wanted.
 */
export function DashboardPage() {
  const { dashboardId = '' } = useParams();
  const navigate = useNavigate();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const dashboardQuery = useDashboard(orgId, dashboardId);
  const saveGadgets = useSaveDashboardGadgets(orgId);

  const [picking, setPicking] = useState(false);
  const [configuring, setConfiguring] = useState<{ def: GadgetDefinition; index?: number } | null>(
    null,
  );
  const [settingsOpen, setSettingsOpen] = useState(false);

  const detail = dashboardQuery.data;
  const canEdit = detail?.is_owner ?? false;

  function writeLayout(gadgets: GadgetRequest[]) {
    saveGadgets.mutate({ dashboardId, gadgets });
  }

  function addGadget(gadget: GadgetRequest) {
    if (!detail) return;
    writeLayout([...toRequests(detail), gadget]);
  }

  function removeGadget(gadgetId: string) {
    if (!detail) return;
    writeLayout(
      detail.gadgets.filter((g) => g.id !== gadgetId).map((g) => ({
        gadget_key: g.gadget_key,
        col_span: g.col_span,
        saved_view_id: g.saved_view_id,
        config: g.config,
      })),
    );
  }


  /**
   * Reorders one tile. The whole collection is rewritten, as every other edit
   * is — the endpoint takes a collection and assigns positions from the array
   * order, so a swap here is a swap there.
   */
  function moveGadget(gadgetId: string, delta: -1 | 1) {
    if (!detail) return;
    const rows = toRequests(detail);
    const index = detail.gadgets.findIndex((g) => g.id === gadgetId);
    const target = index + delta;
    if (index < 0 || target < 0 || target >= rows.length) return;
    [rows[index], rows[target]] = [rows[target], rows[index]];
    writeLayout(rows);
  }

  function openConfigure(gadgetId: string) {
    if (!detail) return;
    const index = detail.gadgets.findIndex((g) => g.id === gadgetId);
    const gadget = detail.gadgets[index];
    const def = gadget && getGadget(gadget.gadget_key);
    if (!def) return;
    setConfiguring({ def, index });
  }

  function saveConfigured(gadget: GadgetRequest) {
    if (!detail || !configuring) return;
    if (configuring.index === undefined) {
      addGadget(gadget);
      return;
    }
    const next = toRequests(detail);
    next[configuring.index] = gadget;
    writeLayout(next);
  }

  if (dashboardQuery.isLoading) {
    return (
      <div className="flex h-32 items-center justify-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
        Loading this dashboard…
      </div>
    );
  }

  if (dashboardQuery.error || !detail) {
    return (
      <div className="space-y-4">
        <BackLink />
        <div
          data-testid="dashboard-error"
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
        >
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(dashboardQuery.error, 'This dashboard is not available to you.')}
          </p>
        </div>
      </div>
    );
  }

  // An invalid dashboard is NOT an error: its audience team was deleted, so it
  // reaches nobody but its owner until they re-scope it. It still opens.
  const scopeNotice = !detail.is_valid && (
    <EmptyState
      icon={LayoutGrid}
      title="Audience unavailable"
      description={
        detail.invalid_reason ||
        'The team this dashboard was shared with no longer exists, so only you can see it.'
      }
      action={
        canEdit ? (
          <Button variant="outline" onClick={() => setSettingsOpen(true)}>
            Change who can see it
          </Button>
        ) : undefined
      }
      className="mb-[var(--space-4)]"
    />
  );

  return (
    <div className="space-y-[var(--space-4)]" data-testid="dashboard-page">
      <BackLink />

      <div className="flex flex-wrap items-center gap-3">
        <h1
          data-testid="dashboard-name"
          className="text-[19px] font-semibold tracking-[-.01em] text-[var(--color-text)]"
        >
          {detail.name}
        </h1>
        <Badge variant="secondary" data-testid="dashboard-visibility">
          {visibilityLabel(detail)}
        </Badge>
        {!detail.is_owner && (
          <Badge variant="outline" data-testid="dashboard-owner">
            {detail.owner_name ? `Shared by ${detail.owner_name}` : 'Shared with you'}
          </Badge>
        )}
        <span className="ml-auto flex items-center gap-2">
          {canEdit && (
            <>
              <Button
                variant="outline"
                data-testid="dashboard-add-gadget"
                onClick={() => setPicking((v) => !v)}
              >
                <Plus className="mr-1.5 h-4 w-4" />
                Add gadget
              </Button>
              <Button
                variant="outline"
                data-testid="dashboard-settings"
                aria-label="Dashboard settings"
                onClick={() => setSettingsOpen(true)}
              >
                <Settings2 className="h-4 w-4" />
              </Button>
            </>
          )}
        </span>
      </div>

      <p className="flex items-center gap-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
        <LayoutGrid className="h-3.5 w-3.5" />
        {detail.gadgets.length} {detail.gadgets.length === 1 ? 'gadget' : 'gadgets'} ·{' '}
        {visibilityLabel(detail)} · each gadget resolves against your own access
      </p>

      {scopeNotice}

      {saveGadgets.error && (
        <div
          data-testid="layout-error"
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-3"
        >
          <AlertCircle className="h-4 w-4 shrink-0 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(saveGadgets.error, 'That layout change was not saved.')}
          </p>
        </div>
      )}

      <div className="grid grid-cols-2 gap-[var(--space-3)] md:grid-cols-4">
        {picking && canEdit && (
          <GadgetPicker
            module={detail.module}
            onPick={(def) => {
              setPicking(false);
              setConfiguring({ def });
            }}
          />
        )}
      </div>

      <DashboardGrid
        gadgets={detail.gadgets}
        orgId={orgId}
        meId={user?.id}
        onRemove={canEdit ? removeGadget : undefined}
        onConfigure={canEdit ? openConfigure : undefined}
        onMove={canEdit ? moveGadget : undefined}
        emptyAction={
          canEdit ? (
            <Button data-testid="empty-add-gadget" onClick={() => setPicking(true)}>
              <Plus className="mr-1.5 h-4 w-4" />
              Add a gadget
            </Button>
          ) : undefined
        }
      />

      {configuring && (
        <GadgetConfigDialog
          open
          onOpenChange={(open) => !open && setConfiguring(null)}
          def={configuring.def}
          orgId={orgId}
          initial={
            configuring.index === undefined ? undefined : toRequests(detail)[configuring.index]
          }
          onSave={saveConfigured}
        />
      )}

      {settingsOpen && (
        <DashboardSettingsDialog
          open
          onOpenChange={setSettingsOpen}
          orgId={orgId}
          dashboard={detail}
          onDeleted={() => navigate('/dashboards')}
        />
      )}
    </div>
  );
}

function visibilityLabel(d: { visibility: string; team_name?: string; is_valid: boolean }): string {
  if (d.visibility === 'org') return 'Everyone in the organisation';
  if (d.visibility === 'team') {
    return d.is_valid && d.team_name ? `Shared with ${d.team_name}` : 'Shared with a deleted team';
  }
  return 'Private to you';
}

function BackLink() {
  return (
    <Link
      to="/dashboards"
      className="inline-flex items-center gap-1 text-[var(--text-xs)] text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
    >
      <ChevronLeft className="h-3.5 w-3.5" />
      All dashboards
    </Link>
  );
}
