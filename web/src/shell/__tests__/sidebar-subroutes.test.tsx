import { render, screen, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { SpaceLayout } from '../SpaceLayout';
import { MODULE_KEYS, type ModuleKey } from '../modules';

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
  // The Codex sidebar owns the scoped page search and the create-page
  // dialog since the navigation collapse (ADR-0005).
  useWikiSearch: vi.fn(() => ({ data: [], isLoading: false })),
  useCreateWikiPage: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false, error: null })),
  // The Beacon sidebar lists the space's queues since P4. `can_manage: false`
  // is the safe default here: this test asserts the sidebar's shape, and the
  // queue controls have their own coverage in BeaconSidebar.queues.test.tsx.
  useQueues: vi.fn(() => ({
    data: { queues: [], can_manage: false },
    isLoading: false,
    error: null,
  })),
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

const SUB_ROUTES = ['board', 'backlog', 'sprints', 'roadmap', 'labels', 'settings'] as const;

/** A nav label that exists only in that module's sidebar. */
const DISTINCTIVE_LABEL: Record<ModuleKey, string> = {
  beacon: 'Tickets',
  codex: 'Pages',
  vector: 'Backlog',
};

const CASES = MODULE_KEYS.flatMap((m) => SUB_ROUTES.map((s) => [m, s] as const));

/**
 * P1 DoD (spec §9): the sidebar is present and correct on EVERY module
 * sub-route — Sprints and Roadmap included. The module is derived from the
 * :module param alone, never from the sub-route.
 */
describe('module sidebar on every sub-route', () => {
  it.each(CASES)('%s sidebar renders on sub-route %s', (module, sub) => {
    render(
      <MemoryRouter initialEntries={[`/${module}/space-1/${sub}`]}>
        <Routes>
          <Route path=":module/:spaceId" element={<SpaceLayout />}>
            {SUB_ROUTES.map((s) => (
              <Route key={s} path={s} element={<div data-testid={`child-${s}`} />} />
            ))}
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    const sidebar = screen.getByTestId('space-sidebar');
    expect(sidebar).toHaveAttribute('data-module', module);
    expect(within(sidebar).getByText(DISTINCTIVE_LABEL[module])).toBeInTheDocument();
    expect(within(sidebar).getByText('Settings')).toBeInTheDocument();
    expect(screen.getByTestId(`child-${sub}`)).toBeInTheDocument();
  });
});

/**
 * C2 gap: the converged tag surface (ModuleLabelsRoute, path `labels`) renders
 * in any module's chrome, but only Vector's sidebar used to carry the way in.
 * Beacon reached it not at all and Codex only through page chips. Every module's
 * sidebar now exposes a "Tags" nav item; a regression that drops it from Beacon
 * or Codex fails here by the missing link. The href pins the route segment to
 * `labels` — the same segment a blank-screen regression (e2e projects.spec)
 * depends on not being renamed in passing.
 */
describe('the Tags nav item is reachable from every module', () => {
  it.each(MODULE_KEYS)('%s sidebar links Tags to its own labels route', (module) => {
    render(
      <MemoryRouter initialEntries={[`/${module}/space-1/board`]}>
        <Routes>
          <Route path=":module/:spaceId" element={<SpaceLayout />}>
            {SUB_ROUTES.map((s) => (
              <Route key={s} path={s} element={<div data-testid={`child-${s}`} />} />
            ))}
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    const sidebar = screen.getByTestId('space-sidebar');
    const tags = within(sidebar).getByRole('link', { name: 'Tags' });
    expect(tags).toHaveAttribute('href', `/${module}/space-1/labels`);
  });
});
