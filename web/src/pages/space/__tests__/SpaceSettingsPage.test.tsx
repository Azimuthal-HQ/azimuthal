import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { SpaceSettingsPage } from '../SpaceSettingsPage';

vi.mock('../../../lib/api', () => {
  const mutationStub = () => ({
    mutate: vi.fn(),
    mutateAsync: vi.fn(),
    isPending: false,
    error: null,
    reset: vi.fn(),
  });
  return {
    useSpace: vi.fn(() => ({
      data: {
        id: 'space-1',
        org_id: 'org-1',
        name: 'Test Space',
        slug: 'test-space',
        key: 'TS',
        type: 'beacon',
        description: null,
        is_private: false,
        owner_team_id: 't1',
        visibility: 'org',
        created_at: '',
        updated_at: '',
      },
      isLoading: false,
      error: null,
    })),
    // The grants list is capability-guarded: non-admins get a stable 403.
    useSpaceGrants: vi.fn(() => ({
      data: undefined,
      isLoading: false,
      error: { status: 403, code: 'forbidden', message: 'manage_grants required' },
    })),
    useTeams: vi.fn(() => ({ data: [], isLoading: false })),
    useUpdateSpace: vi.fn(mutationStub),
    useCreateGrant: vi.fn(mutationStub),
    useUpdateGrant: vi.fn(mutationStub),
    useRevokeGrant: vi.fn(mutationStub),
  };
});

vi.mock('../../../lib/auth', () => ({
  useAuth: vi.fn(() => ({
    user: { id: 'u1', email: 'test@example.com', orgId: 'org-1', role: 'member' },
    isAuthenticated: true,
    login: vi.fn(),
    logout: vi.fn(),
  })),
}));

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/beacon/space-1/settings']}>
      <Routes>
        <Route path=":module/:spaceId/settings" element={<SpaceSettingsPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('SpaceSettingsPage grants section', () => {
  it('renders the explicit forbidden state when the grants list 403s', () => {
    renderPage();

    const forbidden = screen.getByTestId('grants-forbidden');
    expect(forbidden).toHaveTextContent('You need manage_grants on this space');

    // No grant rows and no add-grant form leak through the forbidden state.
    expect(screen.queryByTestId('grant-row')).not.toBeInTheDocument();
    expect(screen.queryByTestId('grant-add-button')).not.toBeInTheDocument();
  });

  it('still offers the visibility section alongside the forbidden grants state', () => {
    renderPage();

    expect(screen.getByTestId('visibility-option-hidden')).toBeInTheDocument();
    expect(screen.getByTestId('visibility-option-discoverable')).toBeInTheDocument();
    expect(screen.getByTestId('visibility-option-org')).toBeInTheDocument();
  });
});
