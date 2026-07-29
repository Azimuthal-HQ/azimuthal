import { useState } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { AlertCircle, Compass, LayoutGrid, Plus, Settings2 } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { DashboardGrid } from '../../components/dashboards/DashboardGrid';
import { GadgetPicker } from '../../components/dashboards/GadgetPicker';
import { GadgetConfigDialog } from '../../components/dashboards/GadgetConfigDialog';
import { CreateSpaceDialog } from './CreateSpaceDialog';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useHomeDashboard,
  useSaveDashboardGadgets,
  useSpaces,
  type DashboardDetail,
  type GadgetRequest,
} from '../../lib/api';
import { MODULE_KEYS, MODULES, type ModuleKey } from '../../shell/modules';
import { getGadget, type GadgetDefinition } from '../../lib/dashboards/registry';

const MODULE_TAGLINE: Record<ModuleKey, string> = {
  beacon: 'track and resolve customer issues',
  codex: "document your team's knowledge",
  vector: 'plan and track work with sprints and backlogs',
};

function toRequests(detail: DashboardDetail): GadgetRequest[] {
  return detail.gadgets.map((g) => ({
    gadget_key: g.gadget_key,
    col_span: g.col_span,
    saved_view_id: g.saved_view_id,
    config: g.config,
  }));
}

/**
 * Home: the post-login landing (decision log D4).
 *
 * It is now the caller's default Home dashboard. A person who has never opened
 * it gets a starter layout created for them, once, on the visit itself — my
 * work, recently updated, and a note explaining both.
 *
 * WHAT THE INTERIM HOME HAD AND THIS DOES NOT. The four space-count cards and
 * the space-card grid are gone. Neither is expressible as a gadget: the filter
 * vocabulary is over tickets and items, and a space is neither. Both are also
 * duplicates — /spaces is the space directory and the top bar's picker is the
 * navigator — so the loss is a duplicate, not a capability. Recorded in the
 * phase report rather than dropped silently.
 *
 * WHAT SURVIVES DELIBERATELY. The "Welcome back" heading, the Create Space
 * button and the `?create=space` deep link, because the top bar's Create
 * control lands here when the reader is not inside a space, and losing that
 * would make that button silently do nothing. The zero-spaces onboarding also
 * survives: somebody with no spaces has nothing for a dashboard to show, and
 * the first thing they need is a space rather than a gadget.
 */
export function HomePage() {
  const navigate = useNavigate();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const homeQuery = useHomeDashboard(orgId);
  const saveGadgets = useSaveDashboardGadgets(orgId);
  const { data: rawSpaces } = useSpaces(orgId);

  // Locked directory rows are listed-but-unenterable: they belong on /spaces,
  // never on a link from here.
  const spaces = rawSpaces
    ? (Array.isArray(rawSpaces) ? rawSpaces : [rawSpaces]).filter((s) => s.readable !== false)
    : undefined;

  // The top bar's Create button lands here as /?create=space when the reader
  // is not inside a space. It is DERIVED rather than copied into state by an
  // effect: setState in an effect body causes a cascading render, and the
  // eslint gate refuses it. The parameter is cleared when the dialog closes
  // rather than on mount, so a refresh reopens what the reader was doing.
  const [searchParams, setSearchParams] = useSearchParams();
  const [dialogOpen, setDialogOpen] = useState(false);
  const wantsCreateSpace = searchParams.get('create') === 'space';
  const spaceDialogOpen = dialogOpen || wantsCreateSpace;

  function setSpaceDialogOpen(next: boolean) {
    setDialogOpen(next);
    if (!next && wantsCreateSpace) {
      setSearchParams({}, { replace: true });
    }
  }

  const [picking, setPicking] = useState(false);
  const [configuring, setConfiguring] = useState<{ def: GadgetDefinition; index?: number } | null>(
    null,
  );

  const detail = homeQuery.data;

  function writeLayout(gadgets: GadgetRequest[]) {
    if (!detail) return;
    saveGadgets.mutate({ dashboardId: detail.id, gadgets });
  }

  function saveConfigured(gadget: GadgetRequest) {
    if (!detail || !configuring) return;
    if (configuring.index === undefined) {
      writeLayout([...toRequests(detail), gadget]);
      return;
    }
    const next = toRequests(detail);
    next[configuring.index] = gadget;
    writeLayout(next);
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

  const noSpaces = spaces && spaces.length === 0;

  return (
    <div className="space-y-[var(--space-6)]" data-testid="home-page">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-[19px] font-semibold tracking-[-.01em] text-[var(--color-text)]">
            Welcome back
          </h1>
          <p className="mt-0.5 text-[var(--text-sm)] text-[var(--color-text-muted)]">
            {detail
              ? 'Your Home dashboard. Every gadget resolves against your own access.'
              : 'Here is an overview of your work.'}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {detail && (
            <>
              <Button
                variant="outline"
                data-testid="home-add-gadget"
                onClick={() => setPicking((v) => !v)}
              >
                <Plus className="mr-1.5 h-4 w-4" />
                Add gadget
              </Button>
              <Button variant="outline" asChild>
                <Link to={`/dashboards/${detail.id}`} data-testid="home-open-dashboard">
                  <Settings2 className="mr-1.5 h-4 w-4" />
                  Manage
                </Link>
              </Button>
            </>
          )}
          <Button onClick={() => setSpaceDialogOpen(true)}>
            <Plus className="mr-2 h-4 w-4" />
            Create Space
          </Button>
        </div>
      </div>

      {homeQuery.isLoading && (
        <div className="flex h-32 items-center justify-center text-[var(--color-text-muted)]">
          Loading your dashboard...
        </div>
      )}

      {homeQuery.error && (
        <div
          data-testid="home-error"
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
        >
          <AlertCircle className="h-5 w-5 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(homeQuery.error, 'Your dashboard is unavailable right now.')}
          </p>
        </div>
      )}

      {noSpaces && (
        <div
          data-testid="home-onboarding"
          className="flex flex-col items-center justify-center py-12 text-center"
        >
          <div className="mb-6 flex h-24 w-24 items-center justify-center rounded-full bg-[var(--color-primary-muted)]">
            <Compass className="h-12 w-12 text-[var(--color-primary)]" />
          </div>
          <h2 className="text-[var(--text-2xl)] font-bold text-[var(--color-text)]">
            Welcome to Azimuthal
          </h2>
          <p className="mt-2 max-w-md text-[var(--text-sm)] text-[var(--color-text-muted)]">
            You don&apos;t have any spaces yet. Create your first space to get started — your
            dashboard fills up as soon as there is work in it.
          </p>
          <Button size="lg" className="mt-6" onClick={() => setSpaceDialogOpen(true)}>
            <Plus className="mr-2 h-5 w-5" />
            Create your first space
          </Button>
          <div className="mt-8 max-w-md text-left">
            <p className="mb-3 text-[var(--text-sm)] font-medium text-[var(--color-text-muted)]">
              Not sure where to start?
            </p>
            <ul className="space-y-2 text-[var(--text-sm)] text-[var(--color-text-muted)]">
              {MODULE_KEYS.map((key) => {
                const def = MODULES[key];
                return (
                  <li key={key} className="flex items-start gap-2">
                    <def.icon className="mt-0.5 h-4 w-4 shrink-0 text-[var(--color-primary)]" />
                    <span>
                      <strong className="text-[var(--color-text)]">{def.name}</strong> &mdash;{' '}
                      {MODULE_TAGLINE[key]}
                    </span>
                  </li>
                );
              })}
            </ul>
          </div>
        </div>
      )}

      {detail && !noSpaces && (
        <>
          {saveGadgets.error && (
            <div
              data-testid="home-layout-error"
              className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-3"
            >
              <AlertCircle className="h-4 w-4 shrink-0 text-[var(--color-danger)]" />
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
                {friendlyErrorMessage(saveGadgets.error, 'That layout change was not saved.')}
              </p>
            </div>
          )}

          {picking && (
            <div className="grid grid-cols-2 gap-[var(--space-3)] md:grid-cols-4">
              <GadgetPicker
                module="home"
                onPick={(def) => {
                  setPicking(false);
                  setConfiguring({ def });
                }}
              />
            </div>
          )}

          <DashboardGrid
            gadgets={detail.gadgets}
            orgId={orgId}
            meId={user?.id}
            onRemove={(gadgetId) =>
              writeLayout(
                detail.gadgets
                  .filter((g) => g.id !== gadgetId)
                  .map((g) => ({
                    gadget_key: g.gadget_key,
                    col_span: g.col_span,
                    saved_view_id: g.saved_view_id,
                    config: g.config,
                  })),
              )
            }
            onMove={moveGadget}
            onConfigure={(gadgetId) => {
              const index = detail.gadgets.findIndex((g) => g.id === gadgetId);
              const def = getGadget(detail.gadgets[index]?.gadget_key ?? '');
              if (def) setConfiguring({ def, index });
            }}
            emptyAction={
              <Button data-testid="home-empty-add" onClick={() => setPicking(true)}>
                <LayoutGrid className="mr-1.5 h-4 w-4" />
                Add a gadget
              </Button>
            }
          />
        </>
      )}

      {configuring && detail && (
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

      <CreateSpaceDialog
        open={spaceDialogOpen}
        onOpenChange={setSpaceDialogOpen}
        orgId={orgId}
        onCreated={(path) => navigate(path)}
      />
    </div>
  );
}
