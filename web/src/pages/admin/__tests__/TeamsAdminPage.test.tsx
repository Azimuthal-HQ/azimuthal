import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { TeamsAdminPage } from '../TeamsAdminPage';

// S10(a): the create-team dialog offers opt-in per-module space checkboxes
// (default unchecked). Creating with Codex checked passes modules: ['codex']
// to the orchestration hook, which creates the space + team grant.

const createMutate = vi.fn();

vi.mock('../../../lib/api', () => ({
  friendlyErrorMessage: (_e: unknown, f: string) => f,
  useTeams: () => ({ data: [], isLoading: false, error: null }),
  useCreateTeamWithSpaces: () => ({ mutate: createMutate, reset: vi.fn(), isPending: false, error: null }),
  useUpdateTeam: () => ({ mutate: vi.fn(), reset: vi.fn(), isPending: false, error: null }),
  useDeleteTeam: () => ({ mutate: vi.fn(), isPending: false, error: null }),
  usePutTeamMember: () => ({ mutate: vi.fn(), isPending: false }),
  useRemoveTeamMember: () => ({ mutate: vi.fn(), isPending: false }),
  useTeamMembers: () => ({ data: [], isLoading: false }),
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'u1', orgId: 'org-1', role: 'admin' } }),
}));

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/admin/teams']}>
      <TeamsAdminPage />
    </MemoryRouter>,
  );
}

describe('TeamsAdminPage — auto-space at team creation (S10a)', () => {
  it('defaults the module checkboxes unchecked and passes only checked modules', () => {
    createMutate.mockReset();
    renderPage();

    fireEvent.click(screen.getByTestId('team-create-button'));

    // Default unchecked.
    expect(screen.getByTestId('team-create-space-beacon')).not.toBeChecked();
    expect(screen.getByTestId('team-create-space-codex')).not.toBeChecked();
    expect(screen.getByTestId('team-create-space-vector')).not.toBeChecked();

    fireEvent.change(screen.getByTestId('team-create-name'), { target: { value: 'DevOps' } });
    fireEvent.click(screen.getByTestId('team-create-space-codex'));
    fireEvent.click(screen.getByTestId('team-create-submit'));

    expect(createMutate).toHaveBeenCalledTimes(1);
    const vars = createMutate.mock.calls[0][0];
    expect(vars.name).toBe('DevOps');
    expect(vars.slug).toBe('devops'); // auto-slugged from the name
    expect(vars.modules).toEqual(['codex']);
  });

  it('passes an empty modules list when no box is checked', () => {
    createMutate.mockReset();
    renderPage();

    fireEvent.click(screen.getByTestId('team-create-button'));
    fireEvent.change(screen.getByTestId('team-create-name'), { target: { value: 'Platform' } });
    fireEvent.click(screen.getByTestId('team-create-submit'));

    expect(createMutate).toHaveBeenCalledTimes(1);
    expect(createMutate.mock.calls[0][0].modules).toEqual([]);
  });
});
