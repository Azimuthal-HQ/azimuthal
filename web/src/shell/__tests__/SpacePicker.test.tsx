import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { installLocalStorageStub } from '../../test/localStorageStub';
import { SpacePicker } from '../SpacePicker';

// The environment's built-in Storage shim lacks setItem; the focus test
// persists a team focus before rendering.
installLocalStorageStub();

// Directory rows (P2 shape): readable spaces owned by a known team, a
// locked row (readable: false), and a space whose owning team is unknown.
vi.mock('../../lib/api', () => ({
  useSpaces: vi.fn(() => ({
    data: [
      {
        id: 's1',
        name: 'Support',
        slug: 'support',
        type: 'beacon',
        owner_team_id: 't1',
        visibility: 'org',
        readable: true,
        effective_role: 'contributor',
      },
      {
        id: 's2',
        name: 'Locked One',
        slug: 'locked-one',
        type: 'beacon',
        owner_team_id: 't1',
        visibility: 'discoverable',
        readable: false,
      },
      {
        id: 's3',
        name: 'Orphan',
        slug: 'orphan',
        type: 'beacon',
        owner_team_id: 't-gone',
        visibility: 'org',
        readable: true,
      },
    ],
    isLoading: false,
  })),
  useTeams: vi.fn(() => ({
    data: [
      {
        id: 't1',
        org_id: 'org-1',
        path: ['t1'],
        slug: 'platform',
        name: 'Platform',
        description: '',
        is_default: false,
        source: 'manual',
        created_at: '',
        updated_at: '',
      },
    ],
    isLoading: false,
  })),
}));

vi.mock('../../lib/auth', () => ({
  useAuth: vi.fn(() => ({
    user: { id: 'u1', email: 'test@example.com', orgId: 'org-1', role: 'member' },
    isAuthenticated: true,
    login: vi.fn(),
    logout: vi.fn(),
  })),
  // useTeamFocus (real) resolves its per-org storage key through this.
  getCurrentOrgId: vi.fn(() => 'org-1'),
}));

function renderPicker() {
  render(
    <MemoryRouter initialEntries={['/beacon/s1/tickets']}>
      <SpacePicker module="beacon" currentSpace={undefined} collapsed={false} />
    </MemoryRouter>,
  );
  fireEvent.click(screen.getByTestId('space-picker-button'));
}

/** ADR-0006 point 6: picker rows group by owning team; locked rows never appear. */
describe('SpacePicker', () => {
  it('groups spaces under their team name and unknown owners under "Other spaces"', () => {
    renderPicker();

    expect(screen.getByText('Platform')).toBeInTheDocument();
    expect(screen.getByText('Support')).toBeInTheDocument();
    expect(screen.getByText('Other spaces')).toBeInTheDocument();
    expect(screen.getByText('Orphan')).toBeInTheDocument();
  });

  it('excludes non-readable (locked) spaces from the picker entirely', () => {
    renderPicker();

    expect(screen.queryByText('Locked One')).not.toBeInTheDocument();
  });

  it('narrows to the focused team but shows the hidden-by-focus count, which clears on click', () => {
    window.localStorage.setItem(
      'azimuthal_team_focus_org-1',
      JSON.stringify({ teamId: 't1', teamName: 'Platform' }),
    );
    renderPicker();

    // Focused: only Platform's spaces are offered…
    expect(screen.getByText('Support')).toBeInTheDocument();
    expect(screen.queryByText('Orphan')).not.toBeInTheDocument();

    // …but the narrowing is never silent (union by default, narrow by choice).
    const hiddenRow = screen.getByTestId('space-picker-focus-hidden');
    expect(hiddenRow).toHaveTextContent('1 space hidden by focus');

    fireEvent.click(hiddenRow);
    expect(screen.getByText('Orphan')).toBeInTheDocument();
    expect(screen.queryByTestId('space-picker-focus-hidden')).not.toBeInTheDocument();
  });
});
