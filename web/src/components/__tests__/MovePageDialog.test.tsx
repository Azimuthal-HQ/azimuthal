import { render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { MovePageDialog } from '../MovePageDialog';

// Regression (PR #63 final feedback, A2): the destination list offered
// Beacon and Vector spaces, and choosing one failed server-side — the
// backend correctly rejects a page move into a non-Codex space, so the
// frontend must never offer one. Fails against the pre-fix filter (which
// checked only readability and self-exclusion), passes with the
// type === 'codex' filter.

vi.mock('../../lib/api', () => ({
  useSpaces: vi.fn(() => ({
    data: [
      { id: 'wiki-1', name: 'Current Wiki', slug: 'current-wiki', type: 'codex', readable: true },
      { id: 'wiki-2', name: 'Other Wiki', slug: 'other-wiki', type: 'codex', readable: true },
      { id: 'wiki-3', name: 'Locked Wiki', slug: 'locked-wiki', type: 'codex', readable: false },
      { id: 'desk-1', name: 'Service Desk', slug: 'service-desk', type: 'beacon', readable: true },
      { id: 'proj-1', name: 'Delivery', slug: 'delivery', type: 'vector', readable: true },
    ],
    isLoading: false,
  })),
  useMoveShareImpact: vi.fn(() => ({ data: { active_share_count: 0 } })),
  useMoveWikiPage: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  friendlyErrorMessage: vi.fn((_err: unknown, fallback: string) => fallback),
}));

describe('MovePageDialog destinations', () => {
  it('offers only readable Codex spaces, excluding the current one', () => {
    render(
      <MovePageDialog
        orgId="org-1"
        spaceId="wiki-1"
        pageId="p1"
        pageTitle="Runbook"
        onClose={vi.fn()}
      />,
    );

    const select = screen.getByLabelText('Destination space');
    const options = within(select).getAllByRole('option');
    const labels = options.map((o) => o.textContent);

    // The placeholder plus exactly the one valid destination.
    expect(labels).toEqual(['Choose a space…', 'Other Wiki']);
    // Never a Beacon or Vector space, never an unreadable or the current wiki.
    expect(labels).not.toContain('Service Desk');
    expect(labels).not.toContain('Delivery');
    expect(labels).not.toContain('Locked Wiki');
    expect(labels).not.toContain('Current Wiki');
  });
});
