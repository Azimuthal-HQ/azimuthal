import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { TaggedPages } from '../../../lib/api';
import { TagPage } from '../TagPage';

/**
 * The tag browse (U4).
 *
 * Two of these three cases are about what the page must NOT say. The results
 * span spaces, so a row that showed only a title would be ambiguous between two
 * pages of the same name in different spaces — and the server filters the
 * results to the spaces the reader can enter, so an empty list is evidence
 * about this reader, not about the tag. A page that reported "this tag is
 * unused" would be stating something it cannot know, to the one person most
 * likely to be looking for a page somebody else told them about.
 */

const { usePagesWithTagMock } = vi.hoisted(() => ({ usePagesWithTagMock: vi.fn() }));

vi.mock('../../../lib/auth', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../../lib/auth')>()),
  getCurrentOrgId: () => 'org-1',
}));

// The importOriginal spread keeps the real APIError class, which the 404 branch
// dispatches on through `error.status`.
vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    usePagesWithTag: usePagesWithTagMock,
  };
});

const RESULT: TaggedPages = {
  tag: {
    id: 'tag-1',
    org_id: 'org-1',
    slug: 'runbook',
    name: 'runbook',
    created_at: '2026-01-01T00:00:00Z',
  },
  pages: [
    {
      page_id: 'p1',
      space_id: 'space-1',
      space_name: 'Platform',
      space_key: 'PLAT',
      title: 'Deploy',
      path: 'deploy',
      updated_at: '2026-07-01T00:00:00Z',
    },
    {
      page_id: 'p2',
      space_id: 'space-2',
      space_name: 'Support',
      space_key: 'SUP',
      title: 'Deploy',
      path: 'deploy',
      updated_at: '2026-07-02T00:00:00Z',
    },
  ],
};

function renderTagPage(label = 'runbook') {
  return render(
    <MemoryRouter initialEntries={[`/codex/space-1/tags/${encodeURIComponent(label)}`]}>
      <Routes>
        <Route path="/codex/:spaceId/tags/:label" element={<TagPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  usePagesWithTagMock.mockReturnValue({ data: RESULT, isLoading: false, error: null });
});

describe('the tag browse', () => {
  it('names the tag and lists a row per page', () => {
    renderTagPage();

    expect(screen.getByTestId('codex-tag-page')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /runbook/ })).toBeInTheDocument();
    expect(screen.getAllByTestId('codex-tag-page-row')).toHaveLength(2);
  });

  it('disambiguates two same-titled pages by their space, and links each into its own space', () => {
    renderTagPage();

    const rows = screen.getAllByTestId('codex-tag-page-row');
    // Both pages are called "Deploy". Without the space name these rows would
    // be indistinguishable, which is the whole reason the row carries it.
    expect(rows[0]).toHaveTextContent('Platform');
    expect(rows[1]).toHaveTextContent('Support');
    // And the link must follow the PAGE's space, not the space in the URL —
    // a href built from the route param would send both rows to space-1.
    expect(rows[0]).toHaveAttribute('href', '/codex/space-1/pages/p1');
    expect(rows[1]).toHaveAttribute('href', '/codex/space-2/pages/p2');
  });

  it('is queried by the label from the URL, decoded', () => {
    renderTagPage('On Call');

    // The label, not a slug: tagLinks.ts puts the display form in the path
    // precisely so the client never reimplements the server's slug rule.
    expect(usePagesWithTagMock).toHaveBeenCalledWith('org-1', 'On Call');
  });

  it('says an empty result is about what this reader can see, not about the tag', () => {
    usePagesWithTagMock.mockReturnValue({
      data: { ...RESULT, pages: [] },
      isLoading: false,
      error: null,
    });
    renderTagPage();

    const text = screen.getByTestId('codex-tag-page').textContent ?? '';
    expect(text).toMatch(/no page you can see carries it/i);
    expect(text).toMatch(/spaces you have access to/i);
    // The claim it must not make. "unused", "no pages", "nobody uses" are all
    // statements about spaces this reader cannot see.
    expect(text).not.toMatch(/unused|not used|nobody/i);
  });

  it('says there is no such tag on a 404, rather than showing an empty list', async () => {
    const { APIError } = await import('../../../lib/api');
    usePagesWithTagMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new APIError(404, {
        error: { code: 'NOT_FOUND', message: 'tag not found', request_id: 'req-1' },
      }),
    });
    renderTagPage('ghost');

    expect(screen.getByTestId('codex-tag-page')).toHaveTextContent('There is no tag called “ghost”');
    // A missing tag and a tag with no visible pages are different answers to
    // different questions, so the empty state must not stand in for the 404.
    expect(screen.queryByText(/no page you can see carries it/i)).not.toBeInTheDocument();
  });
});
