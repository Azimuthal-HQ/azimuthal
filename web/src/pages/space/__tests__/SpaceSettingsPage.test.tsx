import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SpaceSettingsPage } from '../SpaceSettingsPage';
import * as api from '../../../lib/api';

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
    // PersonTeamPicker searches people through this; empty is fine here.
    useMemberSearch: vi.fn(() => ({ data: [], isLoading: false })),
    friendlyErrorMessage: vi.fn((_err: unknown, fallback: string) => fallback),
    useUpdateSpace: vi.fn(mutationStub),
    useCreateGrant: vi.fn(mutationStub),
    useUpdateGrant: vi.fn(mutationStub),
    useRevokeGrant: vi.fn(mutationStub),
    // The space above is a beacon, so PortalSection renders in EVERY test in
    // this file — these hooks must exist or each one is undefined at render.
    // Default to the no-portal 404, the state every fresh space is in;
    // PortalSection's own suite drives the other states.
    usePortalConfig: vi.fn(() => ({
      data: undefined,
      isLoading: false,
      error: { status: 404, code: 'NOT_FOUND', message: 'this space has no customer portal' },
    })),
    useCreatePortal: vi.fn(mutationStub),
    useUpdatePortal: vi.fn(mutationStub),
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
  // Default every test to the capability-guarded 403; tests that need the
  // loaded state override the mock explicitly, so order never matters.
  beforeEach(() => {
    vi.mocked(api.useSpaceGrants).mockReturnValue({
      data: undefined,
      isLoading: false,
      error: { status: 403, code: 'forbidden', message: 'manage_grants required' },
    } as unknown as ReturnType<typeof api.useSpaceGrants>);
  });

  it('renders the explicit forbidden state when the grants list 403s', () => {
    renderPage();

    const forbidden = screen.getByTestId('grants-forbidden');
    expect(forbidden).toHaveTextContent('You need manage_grants on this space');

    // No grant rows and no add-grant form leak through the forbidden state.
    expect(screen.queryByTestId('grant-row')).not.toBeInTheDocument();
    expect(screen.queryByTestId('grant-add-button')).not.toBeInTheDocument();
  });

  // Expectation change (interior restyle): visibility is org-admin-only
  // (set_visibility) and its card lives in the admin panel's space
  // management. Space settings must no longer render it — this test fails
  // if the card is ever re-added here.
  it('renders no visibility card — visibility moved to the admin panel', () => {
    renderPage();

    expect(screen.queryByTestId('visibility-option-hidden')).not.toBeInTheDocument();
    expect(screen.queryByTestId('visibility-option-discoverable')).not.toBeInTheDocument();
    expect(screen.queryByTestId('visibility-option-org')).not.toBeInTheDocument();
    expect(screen.queryByText('Visibility')).not.toBeInTheDocument();
  });

  it('offers the person/team picker instead of a raw UUID field once grants load', () => {
    vi.mocked(api.useSpaceGrants).mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof api.useSpaceGrants>);

    renderPage();

    // The add-grant form leads with the combobox from PersonTeamPicker (P2.5 W5)…
    expect(screen.getByTestId('grant-subject-picker-input')).toBeInTheDocument();
    // …and the free-text UUID field and its old team-select sibling are gone.
    expect(screen.queryByTestId('grant-subject-user-input')).not.toBeInTheDocument();
    expect(screen.queryByTestId('grant-subject-team-select')).not.toBeInTheDocument();
  });

  it('offers the user/team toggle as a segmented radiogroup on the add-grant row', () => {
    vi.mocked(api.useSpaceGrants).mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof api.useSpaceGrants>);

    renderPage();

    const group = screen.getByRole('radiogroup', { name: 'Subject type' });
    expect(group).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'User' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('radio', { name: 'Team' })).toHaveAttribute('aria-checked', 'false');
  });

  // Regression: the picker is controlled, so a selection made under one kind
  // survived flipping the toggle — "Add grant" would then submit a
  // subject_type contradicting the visible toggle.
  it('clears a picked subject when the kind toggle changes', async () => {
    vi.mocked(api.useSpaceGrants).mockReturnValue({
      data: [],
      isLoading: false,
      error: null,
    } as unknown as ReturnType<typeof api.useSpaceGrants>);
    vi.mocked(api.useMemberSearch).mockReturnValue({
      data: [{ id: 'u2', display_name: 'Ada Person', email: 'ada@example.com' }],
      isLoading: false,
    } as unknown as ReturnType<typeof api.useMemberSearch>);

    renderPage();

    // Pick a user while the toggle reads User…
    fireEvent.change(screen.getByTestId('grant-subject-picker-input'), {
      target: { value: 'Ada' },
    });
    fireEvent.click(await screen.findByTestId('grant-subject-picker-option-user-ada@example.com'));
    expect(screen.getByTestId('grant-subject-picker-selected')).toHaveTextContent('Ada Person');
    expect(screen.getByTestId('grant-add-button')).toBeEnabled();

    // …then flip to Team: the stale user selection must not survive.
    fireEvent.click(screen.getByRole('radio', { name: 'Team' }));
    expect(screen.queryByTestId('grant-subject-picker-selected')).not.toBeInTheDocument();
    expect(screen.getByTestId('grant-add-button')).toBeDisabled();
  });
});
