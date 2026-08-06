import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SpaceLayout } from '../SpaceLayout';
import { WikiPage } from '../../pages/codex/WikiPage';

// Regression (review feedback on PR #63, per ADR-0005): a Codex page view
// rendered TWO navigation panels — the shell sidebar and a pre-P1 in-content
// page panel — each with its own page search and page list. The page search,
// tree, and create affordance must exist exactly once, in the sidebar.
//
// Fails on the pre-collapse branch: the in-content panel contributed a
// second "Pages" header, a second search affordance ("Search pages…"), and
// a second rendering of every page title inside the content area.

vi.mock('../../lib/api', () => ({
  useSpace: vi.fn(() => ({
    data: {
      id: 'space-1',
      org_id: 'org-1',
      name: 'Wiki Space',
      slug: 'wiki-space',
      key: 'WK',
      type: 'codex',
      description: null,
      created_at: '',
      updated_at: '',
    },
    isLoading: false,
  })),
  useSpaces: vi.fn(() => ({ data: [], isLoading: false })),
  useTeams: vi.fn(() => ({ data: [], isLoading: false })),
  useWikiPages: vi.fn(() => ({
    data: [
      { id: 'p1', title: 'Alpha Page', parent_id: null },
      { id: 'p2', title: 'Beta Child', parent_id: 'p1' },
    ],
    isLoading: false,
    error: null,
  })),
  // Behaves like the real scoped search: no data until a 2+ character query
  // arrives (the component gates enabled on the DEBOUNCED value, so this is
  // what proves the debounce actually feeds the fetch).
  useWikiSearch: vi.fn((_spaceId: string, q: string) => ({
    data:
      q.length > 1
        ? q.toLowerCase() === 'alpha'
          ? [{ id: 'p1', title: 'Alpha Page', parent_id: null }]
          : []
        : undefined,
    isLoading: false,
  })),
  useCreateWikiPage: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false, error: null })),
  useWikiPage: vi.fn(() => ({
    data: {
      id: 'p1',
      title: 'Alpha Page',
      content: 'Alpha body text.',
      // A page that has only ever held markdown (migration 036).
      doc: null,
      version: 1,
      path: 'alpha',
      updated_at: '2026-07-22T00:00:00Z',
    },
  })),
  useWikiRevisions: vi.fn(() => ({ data: [], isLoading: false })),
  // WikiPage renders the page's tag chips; PageTags fetches its own.
  useEntityTags: () => ({ data: [], isLoading: false, error: null }),
  // The Codex document surface (issue #15). `doc: null` above makes this page
  // a legacy markdown page, so the read path here is the markdown one and the
  // document query stays disabled — which is what keeps this a navigation
  // test rather than an editor one.
  usePageDocument: vi.fn(() => ({
    data: undefined,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  })),
  useSpaceDrafts: vi.fn(() => ({ data: [] })),
  useMe: vi.fn(() => ({ data: { id: 'u1', org_id: 'org-1', display_name: 'Test User' } })),
  useComments: vi.fn(() => ({ data: [], refetch: vi.fn() })),
  useCreateComment: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useEffectiveAccess: vi.fn(() => ({ data: { org_admin: true, role: 'space_admin' } })),
  useSpacePageShares: vi.fn(() => ({ data: [] })),
  pageShareState: vi.fn(() => ({ shared: false, viaCascade: false })),
  friendlyErrorMessage: vi.fn((_err: unknown, fallback: string) => fallback),
}));

vi.mock('../../lib/auth', () => ({
  useAuth: vi.fn(() => ({
    user: { id: 'u1', email: 'test@example.com', orgId: 'org-1', role: 'member' },
    isAuthenticated: true,
    login: vi.fn(),
    logout: vi.fn(),
  })),
  getCurrentOrgId: vi.fn(() => 'org-1'),
}));

// The editor mounts only in edit mode; stub it so the suite does not load
// TipTap for a navigation test. (The stub target moved with issue #15: the
// markdown editor this replaced is gone, and the document editor is a
// different component in a different place.)
vi.mock('../../components/codex/PageEditor', () => ({
  PageEditor: () => <div data-testid="codex-page-editor-stub" />,
}));

function renderCodexPageView() {
  return render(
    <MemoryRouter initialEntries={['/codex/space-1/pages/p1']}>
      <Routes>
        <Route path=":module/:spaceId" element={<SpaceLayout />}>
          <Route path="pages/:pageId" element={<WikiPage />} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );
}

describe('Codex single navigation panel (ADR-0005)', () => {
  it('renders exactly one page search affordance', () => {
    renderCodexPageView();

    // One search input — the sidebar's scoped search…
    const searchInputs = screen.getAllByPlaceholderText(/search/i);
    expect(searchInputs).toHaveLength(1);
    expect(searchInputs[0]).toHaveAccessibleName('Search this wiki');
    // …and no second search affordance disguised as a button (the old
    // sidebar row that navigated to the placeholder route).
    expect(screen.queryByRole('button', { name: /search/i })).not.toBeInTheDocument();
  });

  it('renders exactly one page tree, inside the sidebar', () => {
    renderCodexPageView();

    // One tree container, one "Pages" zone header.
    expect(screen.getAllByTestId('codex-page-tree')).toHaveLength(1);
    expect(screen.getAllByText('Pages')).toHaveLength(1);

    // Every tree row lives inside the sidebar — none in the content area.
    const sidebar = screen.getByTestId('space-sidebar');
    const treeRows = document.querySelectorAll('[data-tree-depth]');
    expect(treeRows.length).toBe(2);
    treeRows.forEach((row) => expect(sidebar.contains(row)).toBe(true));

    // Each page appears exactly once in navigation, hierarchy intact.
    expect(within(sidebar).getAllByText('Alpha Page')).toHaveLength(1);
    expect(within(sidebar).getAllByText('Beta Child')).toHaveLength(1);
    expect(
      within(sidebar).getByText('Beta Child').closest('[data-tree-depth]'),
    ).toHaveAttribute('data-tree-depth', '1');
  });

  it('keeps the create affordance on the tree zone header, once', () => {
    renderCodexPageView();

    const sidebar = screen.getByTestId('space-sidebar');
    const createButtons = screen.getAllByRole('button', { name: 'New page' });
    expect(createButtons).toHaveLength(1);
    expect(sidebar.contains(createButtons[0])).toBe(true);
  });

  // Regression (adversarial review): the tree rendered from the LAYOUT
  // route, whose useParams never sees the child :pageId, so a params-based
  // highlight was always false — and the panel that DID highlight was
  // deleted. NavLink derives active from the location; aria-current is the
  // observable contract.
  it('highlights the current page in the tree', () => {
    renderCodexPageView();

    const sidebar = screen.getByTestId('space-sidebar');
    const active = within(sidebar).getByRole('link', { name: 'Alpha Page' });
    expect(active).toHaveAttribute('aria-current', 'page');
    expect(within(sidebar).getByRole('link', { name: 'Beta Child' })).not.toHaveAttribute(
      'aria-current',
    );
  });
});

describe('Codex scoped search drives the tree zone', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  function searchInput() {
    return screen.getByPlaceholderText('Search this wiki');
  }

  it('typing switches the tree to flat results through the debounce, and clearing restores hierarchy', () => {
    vi.useFakeTimers();
    renderCodexPageView();
    const tree = screen.getByTestId('codex-page-tree');

    // Baseline: hierarchy, child at depth 1.
    expect(within(tree).getByText('Beta Child').closest('[data-tree-depth]')).toHaveAttribute(
      'data-tree-depth',
      '1',
    );

    // Typing 2+ characters flips the tree zone to search mode immediately —
    // the fetch is still gated on the debounced value, so it reads
    // "Searching…" until the debounce fires.
    fireEvent.change(searchInput(), { target: { value: 'alpha' } });
    expect(within(tree).getByText('Searching…')).toBeInTheDocument();
    expect(within(tree).queryByText('Beta Child')).not.toBeInTheDocument();

    // The debounce delivers the query to the search hook and flat results
    // replace the placeholder. Would fail if the debounced update were
    // dropped, the debounced/raw props swapped, or the enabled gate changed.
    act(() => {
      vi.advanceTimersByTime(300);
    });
    const result = within(tree).getByText('Alpha Page').closest('[data-tree-depth]');
    expect(result).toHaveAttribute('data-tree-depth', '0');
    expect(within(tree).queryByText('Searching…')).not.toBeInTheDocument();
    expect(within(tree).queryByText('Beta Child')).not.toBeInTheDocument();

    // Clearing the query restores the hierarchy without waiting for the
    // debounce — search mode keys off the live input.
    fireEvent.change(searchInput(), { target: { value: '' } });
    expect(within(tree).getByText('Beta Child').closest('[data-tree-depth]')).toHaveAttribute(
      'data-tree-depth',
      '1',
    );
  });

  it('shows the empty state for a query with no matches', () => {
    vi.useFakeTimers();
    renderCodexPageView();
    const tree = screen.getByTestId('codex-page-tree');

    fireEvent.change(searchInput(), { target: { value: 'zzzz' } });
    act(() => {
      vi.advanceTimersByTime(300);
    });
    expect(within(tree).getByText('No results.')).toBeInTheDocument();
    expect(within(tree).queryByText('Alpha Page')).not.toBeInTheDocument();
  });
});
