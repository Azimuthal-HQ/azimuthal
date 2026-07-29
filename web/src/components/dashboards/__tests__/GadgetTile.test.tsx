import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { GadgetTile } from '../GadgetTile';
import { DashboardGrid } from '../DashboardGrid';
import type { DashboardGadget, GadgetState } from '../../../lib/api';

/**
 * ADR-0009's four degradation rules, drawn.
 *
 * Every one of them is a tile that renders CONTENT: the dashboard always
 * loads, and a broken gadget is never allowed to take the page with it. The
 * data hooks are stubbed because none of these states reaches one — a tile
 * that fetched before checking its state would be asking the server about a
 * view its reader may not see.
 */
vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useGadgetResults: () => ({ data: undefined, isLoading: true, error: null }),
    useGadgetAggregate: () => ({ data: undefined, isLoading: true, error: null }),
  };
});

function gadget(overrides: Partial<DashboardGadget> = {}): DashboardGadget {
  return {
    id: 'g1',
    gadget_key: 'my_work',
    position: 0,
    col_span: 2,
    saved_view_id: null,
    config: {},
    state: 'ready' as GadgetState,
    title: 'My work',
    render: 'list',
    ...overrides,
  };
}

function renderTile(g: DashboardGadget, props: Partial<Parameters<typeof GadgetTile>[0]> = {}) {
  return render(
    <MemoryRouter>
      <GadgetTile gadget={g} orgId="org-1" {...props} />
    </MemoryRouter>,
  );
}

describe('GadgetTile degradation states', () => {
  // Decision log C5. An INERT, LABELLED placeholder — the key is shown so the
  // owner can tell what the tile stood for and remove it deliberately.
  it('renders a labelled placeholder for a key this build does not know', () => {
    renderTile(
      gadget({ gadget_key: 'sprint_burndown', state: 'unknown_gadget', title: '', render: undefined }),
    );

    expect(screen.getByTestId('gadget-unknown')).toBeInTheDocument();
    // Twice on purpose: the heading falls back to the key when there is no
    // title, and the body names it again so the tile explains itself.
    expect(screen.getAllByText(/sprint_burndown/).length).toBeGreaterThan(0);
    expect(screen.getByTestId('gadget-title')).toHaveTextContent('sprint_burndown');
    // The tile still exists, in its slot, with its own frame.
    expect(screen.getByTestId('gadget-tile')).toHaveAttribute('data-gadget-state', 'unknown_gadget');
  });

  // Decision log C2. The private view's name and query never reached the
  // client, so there is nothing here to leak — and the copy must not say what
  // the view was called.
  it('renders "not available to you" without naming the view', () => {
    renderTile(
      gadget({
        gadget_key: 'view_results',
        state: 'view_unreadable',
        title: 'View results',
        saved_view_id: 'v1',
      }),
    );

    expect(screen.getByTestId('gadget-unreadable')).toBeInTheDocument();
    expect(screen.getByText(/not available to you/i)).toBeInTheDocument();
  });

  // The fourth degradation rule: recoverable, and the recovery is offered only
  // to somebody who can take it.
  it('offers to pick another view when the gadget lost its own — to the owner only', () => {
    const onConfigure = vi.fn();
    const { unmount } = renderTile(
      gadget({ gadget_key: 'view_results', state: 'view_required', title: 'View results' }),
      { onConfigure },
    );
    expect(screen.getByTestId('gadget-view-required')).toBeInTheDocument();
    expect(screen.getByTestId('gadget-pick-view')).toBeInTheDocument();
    unmount();

    renderTile(gadget({ gadget_key: 'view_results', state: 'view_required', title: 'View results' }));
    expect(screen.getByTestId('gadget-view-required')).toBeInTheDocument();
    expect(screen.queryByTestId('gadget-pick-view')).toBeNull();
  });

  // Case C1 reaching a gadget. invalid_reason is server-written and shown
  // verbatim: nothing failed, so it must never go through an error path.
  it('shows the scope reason verbatim rather than an error', () => {
    renderTile(
      gadget({
        gadget_key: 'view_results',
        state: 'scope_unavailable',
        title: 'Open bugs',
        invalid_reason: 'every space this view is scoped to has been deleted',
      }),
    );

    expect(
      screen.getByText('every space this view is scoped to has been deleted'),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('gadget-error')).toBeNull();
  });

  it('draws a ready gadget through its registered body', () => {
    renderTile(gadget({ state: 'ready' }));
    // The list body renders ViewResultList, which announces itself while it
    // resolves. The point is that a REGISTERED body was reached at all.
    expect(screen.getByTestId('gadget-tile')).toHaveAttribute('data-gadget-state', 'ready');
    expect(screen.queryByTestId('gadget-unknown')).toBeNull();
  });

  // The edit affordances belong to the owner. A reader of a shared dashboard
  // gets the content and none of the controls.
  it('shows remove and configure only when handlers are given', () => {
    const { unmount } = renderTile(gadget(), { onRemove: vi.fn(), onConfigure: vi.fn() });
    expect(screen.getByTestId('gadget-remove')).toBeInTheDocument();
    expect(screen.getByTestId('gadget-configure')).toBeInTheDocument();
    unmount();

    renderTile(gadget());
    expect(screen.queryByTestId('gadget-remove')).toBeNull();
    expect(screen.queryByTestId('gadget-configure')).toBeNull();
  });

  // Tailwind scans source text for class names, so a computed
  // `col-span-${n}` emits no CSS and every tile would silently render one
  // column wide. The literals are asserted rather than trusted.
  it('maps every stored span to a literal grid class', () => {
    const { unmount: u1 } = renderTile(gadget({ col_span: 1 }));
    expect(screen.getByTestId('gadget-tile').className).toContain('md:col-span-1');
    u1();

    const { unmount: u2 } = renderTile(gadget({ col_span: 2 }));
    expect(screen.getByTestId('gadget-tile').className).toContain('col-span-2');
    u2();

    renderTile(gadget({ col_span: 4 }));
    expect(screen.getByTestId('gadget-tile').className).toContain('md:col-span-4');
  });

  it('renders a note gadget as markdown, never as markup', () => {
    renderTile(
      gadget({
        gadget_key: 'note',
        render: 'note',
        title: 'Note',
        config: { body: '# Heading\n\n<script>alert(1)</script>' },
      }),
    );

    const note = screen.getByTestId('gadget-note');
    expect(note.querySelector('h1')).not.toBeNull();
    // react-markdown escapes embedded HTML, and nothing here re-enables it.
    // A note lands on somebody else's dashboard the moment it is shared.
    expect(note.querySelector('script')).toBeNull();
    expect(note.textContent).toContain('<script>');
  });

  it('tells an empty note apart from a missing one', () => {
    renderTile(gadget({ gadget_key: 'note', render: 'note', title: 'Note', config: {} }));
    expect(screen.getByText(/this note is empty/i)).toBeInTheDocument();
  });
});

describe('DashboardGrid', () => {
  it('renders the prototype empty state with the owner-only action', () => {
    const { unmount } = render(
      <MemoryRouter>
        <DashboardGrid gadgets={[]} orgId="org-1" emptyAction={<button>Add a gadget</button>} />
      </MemoryRouter>,
    );
    expect(screen.getByText(/nothing on this dashboard yet/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /add a gadget/i })).toBeInTheDocument();
    unmount();

    render(
      <MemoryRouter>
        <DashboardGrid gadgets={[]} orgId="org-1" />
      </MemoryRouter>,
    );
    expect(screen.queryByRole('button', { name: /add a gadget/i })).toBeNull();
  });

  // One broken tile must not take the others with it — that is the whole
  // content of "the dashboard still loads".
  it('renders every tile even when one of them is unknown', () => {
    render(
      <MemoryRouter>
        <DashboardGrid
          orgId="org-1"
          gadgets={[
            gadget({ id: 'a', state: 'ready' }),
            gadget({ id: 'b', gadget_key: 'burndown', state: 'unknown_gadget', render: undefined }),
            gadget({ id: 'c', state: 'ready' }),
          ]}
        />
      </MemoryRouter>,
    );
    expect(screen.getAllByTestId('gadget-tile')).toHaveLength(3);
    expect(screen.getByTestId('gadget-unknown')).toBeInTheDocument();
  });
});
