import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { HomePage } from '../HomePage';
import type { DashboardDetail } from '../../../lib/api';

/**
 * Home is now the caller's default dashboard.
 *
 * These pin the three things the replacement had to keep — the heading, the
 * Create Space button and the `?create=space` deep link the top bar depends on
 * — and the one it had to add: the starter layout rendering as tiles.
 */
const useHomeDashboardMock = vi.fn();
const useSpacesMock = vi.fn();
const saveGadgetsMock = vi.fn();

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useHomeDashboard: () => useHomeDashboardMock(),
    useSpaces: () => useSpacesMock(),
    useSaveDashboardGadgets: () => ({ mutate: saveGadgetsMock, error: null, isPending: false }),
    useCreateSpace: () => ({ error: null, isPending: false, mutateAsync: vi.fn() }),
    useGadgetResults: () => ({ data: undefined, isLoading: true, error: null }),
    useGadgetAggregate: () => ({ data: undefined, isLoading: true, error: null }),
  };
});

vi.mock('../../../lib/auth', () => ({
  useAuth: vi.fn(() => ({
    user: { id: 'u1', email: 'test@example.com', orgId: 'org-1', role: 'member' },
    isAuthenticated: true,
    login: vi.fn(),
    logout: vi.fn(),
  })),
  getCurrentOrgId: vi.fn(() => 'org-1'),
}));

function starter(): DashboardDetail {
  return {
    id: 'd1',
    owner_id: 'u1',
    name: 'My work',
    description: '',
    module: 'home',
    is_default: true,
    is_seeded: true,
    visibility: 'private',
    visibility_team_id: null,
    is_owner: true,
    is_valid: true,
    created_at: '2026-07-29T00:00:00Z',
    updated_at: '2026-07-29T00:00:00Z',
    gadgets: [
      {
        id: 'g1',
        gadget_key: 'my_work',
        position: 0,
        col_span: 2,
        saved_view_id: null,
        config: {},
        state: 'ready',
        title: 'My work',
        render: 'list',
      },
      {
        id: 'g2',
        gadget_key: 'note',
        position: 1,
        col_span: 4,
        saved_view_id: null,
        config: { body: '### Welcome' },
        state: 'ready',
        title: 'Note',
        render: 'note',
      },
    ],
  };
}

function renderHome(entry = '/') {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <HomePage />
    </MemoryRouter>,
  );
}

describe('HomePage', () => {
  beforeEach(() => {
    useHomeDashboardMock.mockReset();
    useSpacesMock.mockReset();
    useSpacesMock.mockReturnValue({ data: [{ id: 's1', type: 'beacon', readable: true }] });
    useHomeDashboardMock.mockReturnValue({ data: starter(), isLoading: false, error: null });
  });

  // Four E2E specs assert this heading on `/`, and the top bar's Create
  // control lands on this page. Both had to survive the replacement.
  it('keeps the heading and the Create Space button', () => {
    renderHome();
    expect(screen.getByRole('heading', { name: 'Welcome back' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /create space/i })).toBeInTheDocument();
  });

  it('renders the dashboard gadgets as tiles', () => {
    renderHome();
    expect(screen.getAllByTestId('gadget-tile')).toHaveLength(2);
    expect(screen.getByTestId('gadget-note')).toBeInTheDocument();
  });

  // The deep link is derived from the URL rather than copied into state by an
  // effect — the eslint gate refuses setState in an effect body, and an
  // effect would also re-open the dialog on every re-render.
  it('opens the create-space dialog from ?create=space', () => {
    renderHome('/?create=space');
    expect(screen.getByRole('heading', { name: /create a new space/i })).toBeInTheDocument();
  });

  // Somebody with no spaces has nothing for a dashboard to show. The first
  // thing they need is a space, not a gadget.
  it('shows the onboarding empty state when the reader has no spaces', () => {
    useSpacesMock.mockReturnValue({ data: [] });
    renderHome();
    expect(screen.getByTestId('home-onboarding')).toBeInTheDocument();
    expect(screen.queryByTestId('dashboard-grid')).toBeNull();
  });

  // A locked directory row is listed-but-unenterable, so it must not count as
  // "you have a space" either.
  it('treats unreadable spaces as no spaces', () => {
    useSpacesMock.mockReturnValue({ data: [{ id: 's1', type: 'beacon', readable: false }] });
    renderHome();
    expect(screen.getByTestId('home-onboarding')).toBeInTheDocument();
  });

  it('renders an error panel rather than a blank page when Home is unavailable', () => {
    useHomeDashboardMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error('boom'),
    });
    renderHome();
    expect(screen.getByTestId('home-error')).toBeInTheDocument();
  });
});
