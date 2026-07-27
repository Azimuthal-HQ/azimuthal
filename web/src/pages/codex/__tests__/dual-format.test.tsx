import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { CodexEditableDocument, WikiPage as WikiPageType } from '../../../lib/api';
import { WikiPage } from '../WikiPage';

/**
 * Migration 036's dual-format contract, from the reading surface's side.
 *
 * > `doc IS NULL` — the page predates the document editor. `content` is the
 * > source of truth and is markdown, exactly as before. The old renderer keeps
 * > working, unchanged.
 *
 * Both halves are asserted, and the negative half of each matters more than
 * the positive one: a legacy page must NOT be routed through the document
 * renderer (its stored markdown would have to be converted client-side to get
 * there, which is the lossy direction nothing is allowed to take), and a
 * document page must NOT be rendered from `content` (which is a derived
 * projection for search, not the document).
 *
 * The third test covers conversion entry: opening a legacy page in the editor
 * is what asks the server to convert it, and nothing is written until publish.
 */

const { usePageDocumentMock } = vi.hoisted(() => ({ usePageDocumentMock: vi.fn() }));

const LEGACY_PAGE: WikiPageType = {
  id: 'p1',
  space_id: 'space-1',
  title: 'Legacy Runbook',
  content: '## Old heading\n\nSome **markdown** body.',
  doc: null,
  version: 3,
  parent_id: null,
  author_id: 'u1',
  path: 'legacy',
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
};

const DOCUMENT_PAGE: WikiPageType = {
  ...LEGACY_PAGE,
  id: 'p2',
  title: 'Document Runbook',
  // The derived markdown projection. If the reading surface ever renders THIS
  // for a document page, this string is what would appear.
  content: 'DERIVED PROJECTION — must not be rendered',
  doc: { type: 'doc', content: [] },
};

const EDITABLE: CodexEditableDocument = {
  page_id: 'p2',
  title: 'Document Runbook',
  doc: {
    type: 'doc',
    content: [{ type: 'paragraph', content: [{ type: 'text', text: 'the real document body' }] }],
  },
  base_version: 3,
  source_format: 'document',
  preserved_ids: [],
  draft: null,
};

let currentPage: WikiPageType = LEGACY_PAGE;

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useWikiPages: () => ({ data: [currentPage], isLoading: false, error: null }),
    useWikiPage: () => ({ data: currentPage }),
    useWikiRevisions: () => ({ data: [], isLoading: false }),
    usePageDocument: usePageDocumentMock,
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

function renderPage(pageId: string) {
  return render(
    <MemoryRouter initialEntries={[`/codex/space-1/pages/${pageId}`]}>
      <Routes>
        <Route path="/codex/:spaceId/pages/:pageId" element={<WikiPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  usePageDocumentMock.mockReturnValue({
    data: undefined,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  });
});

describe('a legacy markdown page (doc IS NULL)', () => {
  beforeEach(() => {
    currentPage = LEGACY_PAGE;
  });

  it('renders through the markdown path, exactly as before', async () => {
    renderPage('p1');
    expect(await screen.findByText('Old heading')).toBeInTheDocument();
    expect(screen.getByText('markdown')).toBeInTheDocument();
  });

  it('is not routed through the document renderer', () => {
    renderPage('p1');
    expect(screen.queryByTestId('codex-document')).not.toBeInTheDocument();
  });

  it('does not fetch a document just to read the page', () => {
    // Reading a legacy page must not call the conversion route. Converting on
    // every read would make a hot path do work that only an edit needs.
    renderPage('p1');
    const [, , opts] = usePageDocumentMock.mock.calls[0];
    expect(opts.enabled).toBe(false);
  });

  it('asks the server to convert it when the editor is opened', async () => {
    // Conversion is per-page and on first edit (migration 036) — opening the
    // editor is the trigger, and nothing is written until publish.
    renderPage('p1');
    fireEvent.click(screen.getByRole('button', { name: /^Edit$/ }));

    await waitFor(() => {
      const latest = usePageDocumentMock.mock.calls.at(-1);
      expect(latest?.[2].enabled).toBe(true);
    });
  });
});

describe('a document-backed page (doc IS NOT NULL)', () => {
  beforeEach(() => {
    currentPage = DOCUMENT_PAGE;
    usePageDocumentMock.mockReturnValue({
      data: EDITABLE,
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
  });

  it('renders the shielded document, not the derived markdown projection', async () => {
    renderPage('p2');
    expect(await screen.findByTestId('codex-document')).toHaveTextContent(
      'the real document body',
    );
    // `pages.content` exists for the search index and legacy readers. Nothing
    // reads it back into the reading surface when `doc` is present.
    expect(screen.queryByText(/DERIVED PROJECTION/)).not.toBeInTheDocument();
  });

  it('fetches the shielded document rather than using the page’s stored one', () => {
    // WikiPage.doc is the raw stored document and still contains types outside
    // the editor's schema; ProseMirror would drop them silently.
    renderPage('p2');
    const [spaceId, pageId, opts] = usePageDocumentMock.mock.calls[0];
    expect(spaceId).toBe('space-1');
    expect(pageId).toBe('p2');
    expect(opts.enabled).toBe(true);
  });
});
