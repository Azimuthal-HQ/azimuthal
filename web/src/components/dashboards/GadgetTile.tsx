import { ArrowLeft, ArrowRight, Compass, HelpCircle, Lock, Settings2, X } from 'lucide-react';
import { Card, CardContent } from '../ui/card';
import { cn } from '../../lib/utils';
import type { DashboardGadget } from '../../lib/api';
import { getGadget } from '../../lib/dashboards/registry';
// Imported for its registration side effect as well as nothing else: the six
// definitions call registerGadget at module scope, and this is the one place
// that guarantees they have.
import './gadgets';

interface GadgetTileProps {
  gadget: DashboardGadget;
  orgId: string;
  meId?: string;
  /** Rendered when the reader owns the dashboard and is editing it. */
  onRemove?: () => void;
  onConfigure?: () => void;
  /**
   * Moves the tile one slot earlier or later. Undefined at the ends of the
   * collection, so the control is absent rather than present-and-inert.
   *
   * Arrow buttons rather than drag-and-drop, on two grounds. The dashboards
   * prototype shows no drag affordance and no reorder handler — its gadgets
   * are added at the end and removed in place. And the repository's own
   * precedent for reordering a small ordered list is
   * pages/space/BoardConfigSection.tsx, which is exactly this: ArrowUp /
   * ArrowDown over a local draft. Introducing @dnd-kit here would be a second
   * pattern for one problem.
   */
  onMove?: (delta: -1 | 1) => void;
  canMoveEarlier?: boolean;
  canMoveLater?: boolean;
}

/**
 * One tile: the chrome, and the dispatch.
 *
 * THE DISPATCH IS A MAP READ. `getGadget(key)` returns a definition or
 * undefined, and the definition draws itself. There is no switch over a gadget
 * key here or anywhere else in the render path — ADR-0009 decision 5 calls one
 * a defect because it closes the extension seam permanently, and the Go half
 * of the registry has a test that fails on one.
 *
 * EVERY DEGRADATION STATE IS SERVER-COMPUTED. This component never asks "may
 * this person see that view" — the answer arrived on the wire as `state`, from
 * the same audience rule the saved-view endpoints apply. A client that
 * re-derived it would be a second implementation of an authorisation rule.
 *
 * NONE OF THE DEGRADED STATES IS AN ERROR. A dashboard always loads: an
 * unreadable gadget, a deleted view, an unavailable scope and an unknown key
 * are four kinds of content, drawn in the tile's own frame, and the rest of
 * the dashboard renders beside them.
 */
export function GadgetTile({
  gadget,
  orgId,
  meId,
  onRemove,
  onConfigure,
  onMove,
  canMoveEarlier,
  canMoveLater,
}: GadgetTileProps) {
  const def = getGadget(gadget.gadget_key);

  return (
    <Card
      data-testid="gadget-tile"
      data-gadget-key={gadget.gadget_key}
      data-gadget-state={gadget.state}
      className={cn('flex min-w-0 flex-col', spanClass(gadget.col_span))}
    >
      <CardContent className="flex flex-1 flex-col p-[var(--space-4)]">
        <div className="mb-3 flex items-center gap-2">
          {def ? (
            <def.icon className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />
          ) : (
            <HelpCircle className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />
          )}
          <span
            data-testid="gadget-title"
            className="truncate text-[11.5px] font-medium text-[var(--color-text-muted)]"
          >
            {gadget.title || gadget.gadget_key}
          </span>
          <span className="ml-auto flex shrink-0 items-center gap-1">
            {onMove && canMoveEarlier && (
              <button
                type="button"
                data-testid="gadget-move-earlier"
                aria-label={`Move ${gadget.title} earlier`}
                onClick={() => onMove(-1)}
                className="rounded p-0.5 text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
              >
                <ArrowLeft className="h-3.5 w-3.5" />
              </button>
            )}
            {onMove && canMoveLater && (
              <button
                type="button"
                data-testid="gadget-move-later"
                aria-label={`Move ${gadget.title} later`}
                onClick={() => onMove(1)}
                className="rounded p-0.5 text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
              >
                <ArrowRight className="h-3.5 w-3.5" />
              </button>
            )}
            {onConfigure && def && (
              <button
                type="button"
                data-testid="gadget-configure"
                aria-label={`Configure ${gadget.title}`}
                onClick={onConfigure}
                className="rounded p-0.5 text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
              >
                <Settings2 className="h-3.5 w-3.5" />
              </button>
            )}
            {onRemove && (
              <button
                type="button"
                data-testid="gadget-remove"
                aria-label={`Remove ${gadget.title}`}
                onClick={onRemove}
                className="rounded p-0.5 text-[var(--color-text-muted)] hover:text-[var(--color-danger)]"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </span>
        </div>

        <div className="min-w-0 flex-1">
          <GadgetBody gadget={gadget} orgId={orgId} meId={meId} onConfigure={onConfigure} />
        </div>
      </CardContent>
    </Card>
  );
}

function GadgetBody({
  gadget,
  orgId,
  meId,
  onConfigure,
}: GadgetTileProps) {
  const def = getGadget(gadget.gadget_key);

  // ADR-0009 case C5. An inert, LABELLED placeholder: the key is shown so a
  // person can tell what the tile stood for and remove it deliberately, rather
  // than finding a blank square.
  if (!def) {
    return (
      <GadgetNotice
        icon={HelpCircle}
        title="Gadget not available"
        body={`This dashboard holds a "${gadget.gadget_key}" gadget, which this version does not know how to draw. Everything else on the dashboard still works.`}
        testId="gadget-unknown"
      />
    );
  }

  // ADR-0009 case C2. The private view's name and query never reached this
  // client, so there is nothing here to leak.
  if (gadget.state === 'view_unreadable') {
    return (
      <GadgetNotice
        icon={Lock}
        title="Not available to you"
        body="This gadget shows a saved view you do not have access to. The rest of the dashboard is unaffected."
        testId="gadget-unreadable"
      />
    );
  }

  // ADR-0009's fourth degradation rule: recoverable, with the recovery
  // offered — but only to somebody who can actually take it.
  if (gadget.state === 'view_required') {
    return (
      <GadgetNotice
        icon={Compass}
        title="This gadget needs a view"
        body="The saved view it showed is gone. Pick another one to bring it back."
        testId="gadget-view-required"
        action={
          onConfigure ? (
            <button
              type="button"
              data-testid="gadget-pick-view"
              onClick={onConfigure}
              className="text-[var(--text-xs)] font-medium text-[var(--color-primary)] hover:underline"
            >
              Choose a view
            </button>
          ) : undefined
        }
      />
    );
  }

  // ADR-0009 case C1 reaching a gadget. invalid_reason is server-written and
  // shown verbatim — nothing failed, so it must never go through
  // friendlyErrorMessage.
  if (gadget.state === 'scope_unavailable') {
    return (
      <GadgetNotice
        icon={Compass}
        title="Scope unavailable"
        body={gadget.invalid_reason || 'The spaces this view is scoped to are no longer available.'}
        testId="gadget-scope-unavailable"
      />
    );
  }

  return <def.Body gadget={gadget} orgId={orgId} meId={meId} />;
}

/**
 * The shared frame for every non-drawing state. One component so the four read
 * alike, and so none of them accidentally borrows a page-level error phrase —
 * assertNoErrors in the E2E harness treats "could not be loaded" as a broken
 * page, and none of these is one.
 */
function GadgetNotice({
  icon: Icon,
  title,
  body,
  testId,
  action,
}: {
  icon: typeof Compass;
  title: string;
  body: string;
  testId: string;
  action?: React.ReactNode;
}) {
  return (
    <div
      data-testid={testId}
      className="flex h-full flex-col items-start justify-center gap-1 rounded-[var(--radius-lg)] border border-dashed border-[var(--color-border)] p-3"
    >
      <span className="flex items-center gap-1.5 text-[var(--text-xs)] font-medium text-[var(--color-text)]">
        <Icon className="h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)]" />
        {title}
      </span>
      <p className="text-[var(--text-xs)] leading-[1.5] text-[var(--color-text-muted)]">{body}</p>
      {action}
    </div>
  );
}

/**
 * The four-column grid's span classes, spelled literally.
 *
 * Tailwind scans source text for class names, so a computed
 * `col-span-${n}` produces no CSS at all — the tile would silently render one
 * column wide whatever its stored span said.
 */
function spanClass(span: number): string {
  if (span === 4) return 'col-span-2 md:col-span-4';
  if (span === 2) return 'col-span-2';
  return 'col-span-2 md:col-span-1';
}
