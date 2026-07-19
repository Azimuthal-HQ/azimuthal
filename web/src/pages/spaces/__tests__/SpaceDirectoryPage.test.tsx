import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { SpaceDirectoryPage } from '../SpaceDirectoryPage';

vi.mock('../../../lib/api', () => ({
  useSpaces: vi.fn(() => ({
    data: [
      {
        id: 's1',
        name: 'Handbook',
        slug: 'handbook',
        type: 'codex',
        owner_team_id: 't1',
        visibility: 'org',
        readable: true,
        effective_role: 'viewer',
      },
      {
        id: 's2',
        name: 'Payroll',
        slug: 'payroll',
        type: 'codex',
        owner_team_id: 't1',
        visibility: 'discoverable',
        readable: false,
      },
    ],
    isLoading: false,
    error: null,
  })),
  useTeams: vi.fn(() => ({
    data: [
      {
        id: 't1',
        org_id: 'org-1',
        path: ['t1'],
        slug: 'people',
        name: 'People',
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

vi.mock('../../../lib/auth', () => ({
  useAuth: vi.fn(() => ({
    user: { id: 'u1', email: 'test@example.com', orgId: 'org-1', role: 'member' },
    isAuthenticated: true,
    login: vi.fn(),
    logout: vi.fn(),
  })),
  getCurrentOrgId: vi.fn(() => 'org-1'),
}));

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/spaces']}>
      <SpaceDirectoryPage />
    </MemoryRouter>,
  );
}

describe('SpaceDirectoryPage', () => {
  it('renders a locked row as inert with the "contact a space admin" copy', () => {
    renderPage();

    // Assert via the testid, never a global text search — page chrome could
    // collide with the copy.
    const locked = screen.getByTestId('locked-space-row');
    expect(locked).toHaveTextContent('Payroll');
    expect(locked).toHaveTextContent('contact a space admin');

    // Locked rows are not links — neither themselves nor inside one.
    expect(locked.tagName).not.toBe('A');
    expect(locked.closest('a')).toBeNull();
  });

  it('renders a readable row as a link into the space', () => {
    renderPage();

    const row = screen.getByTestId('directory-space-row');
    expect(row).toHaveTextContent('Handbook');
    expect(row.tagName).toBe('A');
    expect(row).toHaveAttribute('href', '/codex/s1');
  });

  it('shows the owning team as the group header with a focus control', () => {
    renderPage();

    expect(screen.getByText('People')).toBeInTheDocument();
    expect(screen.getByTestId('directory-focus-button')).toBeInTheDocument();
  });
});
