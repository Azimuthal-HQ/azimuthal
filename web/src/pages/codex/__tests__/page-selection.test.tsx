import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { WikiPage as WikiPageType } from '../../../lib/api';
import { WikiPage } from '../WikiPage';

/**
 * WikiPage's page selection, and the shared measure (U1/U6).
 *
 * ## What this pins, and what it is pinning it against
 *
 * `WikiPage.tsx` carries a comment recording that auto-select was once a
 * `useMemo` and had to become a `useEffect`:
 *
 * > Auto-select first page once pages load (e.g. after a reload with no page
 * > in the URL). This is a side effect, so it belongs in useEffect — a useMemo
 * > runs inconsistently and made the selection (and the comments/content that
 * > depend on it) race on reload.
 *
 * Nothing tested that. A comment describing a defect somebody already fixed is
 * an invitation to reintroduce it, because the next person to look at the
 * effect sees a value derived from `pages` and a lint rule suggesting a memo.
 *
 * The two cases below are the ones a derived `pages[0]?.id` gets WRONG, which
 * is what makes them a regression test rather than a description:
 *
 *  1. A page id in the URL must win over the first page in the list. A memo of
 *     `pages[0]?.id` would show page A while the URL said B.
 *  2. Once a page is selected, a later refetch of the page list must not move
 *     the selection. A memo re-derives on every `pages` identity change, so a
 *     list that came back reordered — after a create, a move, or a background
 *     refetch — would silently swap the page under the reader, taking the
 *     comments and the document with it. That is precisely the "race on reload"
 *     the comment describes.
 *
 * The third case is the positive one the comment is about: pages arriving after
 * mount, with no page in the URL, do select the first one.
 */

const FIRST: WikiPageType = {
  id: 'p1',
  space_id: 'space-1',
  title: 'First Page',
  content: 'first body',
  doc: null,
  version: 1,
  parent_id: null,
  author_id: 'u1',
  path: 'first',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
};

const SECOND: WikiPageType = { ...FIRST, id: 'p2', title: 'Second Page', content: 'second body' };

const { useWikiPagesMock, useWikiPageMock } = vi.hoisted(() => ({
  useWikiPagesMock: vi.fn(),
  useWikiPageMock: vi.fn(),
}));

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useWikiPages: useWikiPagesMock,
    useWikiPage: useWikiPageMock,
    useWikiRevisions: () => ({ data: [], isLoading: false }),
    usePageDocument: () => ({ data: undefined, isLoading: false, error: null, refetch: vi.fn() }),
    useSpaceDrafts: () => ({ data: [] }),
    useMe: () => ({ data: { id: 'u1', org_id: 'org-1', display_name: 'T' } }),
    useComments: () => ({ data: [], refetch: vi.fn() }),
    useCreateComment: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useEffectiveAccess: () => ({ data: { org_admin: false, role: 'member' } }),
    useSpacePageShares: () => ({ data: [] }),
  };
});

vi.mock('../../../components/codex/PageEditor', () => ({
  PageEditor: () => <div data-testid="codex-page-editor-stub" />,
}));

/**
 * The page list, and the page-by-id lookup that follows the selection.
 *
 * `useWikiPage` is keyed on whatever id WikiPage passes it, which is exactly
 * the selection under test — so the rendered title IS the observable, and the
 * assertions below never touch component internals.
 */
function withPages(pages: WikiPageType[]) {
  useWikiPagesMock.mockReturnValue({ data: pages, isLoading: false, error: null });
  useWikiPageMock.mockImplementation((_space: string, pageId: string) => ({
    data: pages.find((p) => p.id === pageId),
  }));
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/codex/:spaceId/pages/:pageId" element={<WikiPage />} />
        <Route path="/codex/:spaceId" element={<WikiPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe('WikiPage auto-selects a page', () => {
  it('selects the first page when the URL names none', async () => {
    // The case the effect exists for: a reload with no page in the URL still
    // has to land the reader on something.
    withPages([FIRST, SECOND]);
    renderAt('/codex/space-1');

    expect(await screen.findByTestId('wiki-page-title')).toHaveTextContent('First Page');
  });

  it('selects a page that arrives after the first render', async () => {
    // Pages load asynchronously, so the first render has none. A selection made
    // only at mount would leave the reader on the empty state forever.
    withPages([]);
    const { rerender } = renderAt('/codex/space-1');
    expect(screen.queryByTestId('wiki-page-title')).not.toBeInTheDocument();

    withPages([FIRST, SECOND]);
    rerender(
      <MemoryRouter initialEntries={['/codex/space-1']}>
        <Routes>
          <Route path="/codex/:spaceId" element={<WikiPage />} />
        </Routes>
      </MemoryRouter>,
    );

    expect(await screen.findByTestId('wiki-page-title')).toHaveTextContent('First Page');
  });

  it('does not override a page named in the URL', async () => {
    // FAILS under the old implementation. `useMemo(() => pages[0]?.id, [pages])`
    // shows "First Page" here, because the derived value has no way to know a
    // selection was already made from the route.
    withPages([FIRST, SECOND]);
    renderAt('/codex/space-1/pages/p2');

    expect(await screen.findByTestId('wiki-page-title')).toHaveTextContent('Second Page');
    expect(screen.queryByText('First Page')).not.toBeInTheDocument();
  });

  it('keeps the selection when the page list is refetched in a different order', async () => {
    // FAILS under the old implementation, and this is the "race on reload" the
    // source comment describes. A selection re-derived from `pages` recomputes
    // on every new list identity, so a background refetch that returned the
    // same pages in a different order would swap the page under the reader —
    // taking the comments and the document that depend on it along.
    //
    // The rerender is the whole test: it has to be the SAME mounted tree
    // receiving new data, because a second `render` would start from no
    // selection at all and would pass under either implementation.
    // A FRESH element each time, deliberately: React bails out of re-rendering
    // a subtree handed the identical element object, so reusing one would make
    // the rerender below a no-op and the test would pass against anything.
    const tree = () => (
      <MemoryRouter initialEntries={['/codex/space-1']}>
        <Routes>
          <Route path="/codex/:spaceId" element={<WikiPage />} />
        </Routes>
      </MemoryRouter>
    );

    withPages([FIRST, SECOND]);
    const { rerender } = render(tree());
    expect(await screen.findByTestId('wiki-page-title')).toHaveTextContent('First Page');

    // The same two pages, reordered. Nothing about the reader's intent changed.
    withPages([SECOND, FIRST]);
    rerender(tree());

    await waitFor(() => {
      expect(screen.getByTestId('wiki-page-title')).toHaveTextContent('First Page');
    });
  });
});

describe('the Codex measure', () => {
  it('bounds the reading view rather than leaving it to fill the window', async () => {
    // U1. The class is asserted rather than a computed width because jsdom does
    // not lay out — but the class is the whole mechanism: it is a max-width and
    // a full width, which is what makes the surface fluid up to the clamp. A
    // test asserting a pixel width here would assert jsdom, not the layout.
    withPages([FIRST]);
    renderAt('/codex/space-1/pages/p1');

    const measure = await screen.findByTestId('codex-measure');
    expect(measure.className).toContain('max-w-[var(--codex-measure)]');
    expect(measure.className).toContain('w-full');
    // The fixed 76ch this replaced. Naming it is what stops somebody
    // reintroducing a second, differently-clamped container beside this one.
    expect(measure.className).not.toContain('76ch');
  });

  it('wraps the reading view and the editor in the same container', async () => {
    // The point of U1: pressing Edit must not reflow every line. Before this,
    // the reader was pinned to 76ch and the editor had no constraint at all, so
    // the same paragraph broke in different places on either side of one click.
    withPages([FIRST]);
    const { rerender } = renderAt('/codex/space-1/pages/p1');
    const readerClass = (await screen.findByTestId('codex-measure')).className;

    rerender(
      <MemoryRouter initialEntries={['/codex/space-1/pages/p1']}>
        <Routes>
          <Route path="/codex/:spaceId/pages/:pageId" element={<WikiPage />} />
        </Routes>
      </MemoryRouter>,
    );
    screen.getByRole('button', { name: /^Edit$/ }).click();

    await waitFor(() => {
      const measures = screen.getAllByTestId('codex-measure');
      for (const measure of measures) {
        expect(measure.className).toBe(readerClass);
      }
    });
  });
});
