import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { SpaceLayout } from '../SpaceLayout';

vi.mock('../../lib/api', () => ({
  useSpace: vi.fn(() => ({
    data: {
      id: 'space-1',
      org_id: 'org-1',
      name: 'Test Space',
      slug: 'test-space',
      key: 'TS',
      type: 'vector',
      description: null,
      created_at: '',
      updated_at: '',
    },
    isLoading: false,
  })),
  useSpaces: vi.fn(() => ({ data: [], isLoading: false })),
  useTeams: vi.fn(() => ({ data: [], isLoading: false })),
  useWikiPages: vi.fn(() => ({ data: [], isLoading: false })),
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

function NavHelper() {
  const navigate = useNavigate();
  return (
    <>
      <button type="button" onClick={() => navigate('/vector/space-1/sprints')}>go-sprints</button>
      <button type="button" onClick={() => navigate('/vector/space-1/roadmap')}>go-roadmap</button>
    </>
  );
}

function renderAt(initialPath: string) {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path=":module/:spaceId" element={<SpaceLayout />}>
          <Route path="board" element={<div data-testid="child-board" />} />
          <Route path="sprints" element={<div data-testid="child-sprints" />} />
          <Route path="roadmap" element={<div data-testid="child-roadmap" />} />
        </Route>
      </Routes>
      <NavHelper />
    </MemoryRouter>,
  );
}

/**
 * P1 DoD (ADR-0005 point 3): SpaceLayout owns the sidebar and must not
 * remount on sub-route change. DOM node identity across navigations is the
 * remount detector — a remounted component gets fresh host nodes.
 */
describe('SpaceLayout', () => {
  it('keeps the same sidebar DOM node across sub-route navigation', () => {
    renderAt('/vector/space-1/board');

    const sidebarBefore = screen.getByTestId('space-sidebar');
    expect(sidebarBefore).toHaveAttribute('data-module', 'vector');
    expect(screen.getByTestId('child-board')).toBeInTheDocument();

    fireEvent.click(screen.getByText('go-sprints'));
    expect(screen.getByTestId('child-sprints')).toBeInTheDocument();
    expect(screen.queryByTestId('child-board')).not.toBeInTheDocument();
    expect(screen.getByTestId('space-sidebar')).toBe(sidebarBefore);

    fireEvent.click(screen.getByText('go-roadmap'));
    expect(screen.getByTestId('child-roadmap')).toBeInTheDocument();
    expect(screen.getByTestId('space-sidebar')).toBe(sidebarBefore);
    expect(screen.getByTestId('space-sidebar')).toHaveAttribute('data-module', 'vector');
  });

  it('renders the not-found state for an unknown module, keeping no sidebar', () => {
    renderAt('/warp/space-1/board');
    expect(screen.queryByTestId('space-sidebar')).not.toBeInTheDocument();
    expect(screen.getByText('Page not found')).toBeInTheDocument();
  });
});
