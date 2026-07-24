import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { RoadmapPage } from '../RoadmapPage';

// S2 regression. Two independent defects:
//  1. WIRING: the roadmap "All Items" endpoint requires from/to query params;
//     the page sent neither, so the API 400'd. The page must now send a
//     computed date window (from & to as YYYY-MM-DD).
//  2. SWALLOWED ERROR: the page only read {data, isLoading}, so an API error
//     rendered as the empty state — indistinguishable from a genuinely empty
//     roadmap. The page must surface an error state distinct from empty.

type RoadmapQuery = {
  data: unknown[] | undefined;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
};

const useRoadmapMock = vi.fn(
  (..._args: unknown[]): RoadmapQuery => ({
    data: [],
    isLoading: false,
    isError: false,
    error: null,
  }),
);

vi.mock('../../../lib/api', () => ({
  useRoadmap: (...args: unknown[]) => useRoadmapMock(...args),
  useRoadmapSprints: () => ({ data: [], isLoading: false, isError: false, error: null }),
  useSprints: () => ({ data: [] }),
  // W3 renders item keys as provenance chips, which needs the space's key.
  useSpace: () => ({ data: { id: 's1', key: 'VEC' } }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

function renderRoadmap() {
  return render(
    <MemoryRouter initialEntries={['/vector/s1/roadmap']}>
      <Routes>
        <Route path="/vector/:spaceId/roadmap" element={<RoadmapPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => {
  useRoadmapMock.mockReset();
  useRoadmapMock.mockReturnValue({ data: [], isLoading: false, isError: false, error: null });
});

describe('RoadmapPage data wiring', () => {
  it('requests the roadmap with a concrete from/to date window', () => {
    renderRoadmap();

    expect(useRoadmapMock).toHaveBeenCalled();
    const args = useRoadmapMock.mock.calls[0];
    expect(args[0]).toBe('s1'); // spaceId
    // from and to must be present, YYYY-MM-DD — without them the API 400s.
    expect(args[1]).toMatch(/^\d{4}-\d{2}-\d{2}$/);
    expect(args[2]).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });

  it('surfaces an API error distinctly from the empty state', () => {
    useRoadmapMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error('boom'),
    });

    renderRoadmap();

    expect(screen.getByText(/unavailable right now|try again/i)).toBeInTheDocument();
    // The "genuinely empty" copy must NOT appear when the request errored.
    expect(screen.queryByText(/No items with due dates/i)).not.toBeInTheDocument();
  });
});
