import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { TeamsAdminPage } from '../TeamsAdminPage';

// S10(a): the create-team dialog offers opt-in per-module space checkboxes
// (default unchecked). Creating with Codex checked passes modules: ['codex']
// to the orchestration hook, which creates the space + team grant.

const createMutate = vi.fn();
const updateMutate = vi.fn();
const deleteMutate = vi.fn();

// One non-default team so the edit and delete surfaces have a row to act on.
const TEAM = {
  id: 't1', name: 'Platform', slug: 'platform', parent_id: null, path: ['t1'],
  is_default: false, description: null,
};

// vi.mock factories are hoisted above module-scope declarations, so the
// required-mode switch these tests flip has to be hoisted with them.
// vi.hoisted is the sanctioned way; a plain `let` here throws
// "Cannot access before initialization" inside the factory.
const deployment = vi.hoisted(() => ({ ticketRefRequired: false }));

vi.mock('../../../lib/api', () => ({
  friendlyErrorMessage: (_e: unknown, f: string) => f,
  useTeams: () => ({ data: [TEAM], isLoading: false, error: null }),
  useCreateTeamWithSpaces: () => ({ mutate: createMutate, reset: vi.fn(), isPending: false, error: null }),
  useUpdateTeam: () => ({ mutate: updateMutate, reset: vi.fn(), isPending: false, error: null }),
  useDeleteTeam: () => ({ mutate: deleteMutate, isPending: false, error: null }),
  usePutTeamMember: () => ({ mutate: vi.fn(), isPending: false }),
  useRemoveTeamMember: () => ({ mutate: vi.fn(), isPending: false }),
  useTeamMembers: () => ({ data: [], isLoading: false }),
  // A3: the create, edit and delete surfaces now carry a TicketRefField.
  useTicketRefSuggestions: () => ({ data: [], isLoading: false, error: null }),
  useTicketRefRequired: () => deployment.ticketRefRequired,
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

// A3: create, edit and delete can carry the operator's ticket reference.
// Before A3 all three sent none. Each test pins the mutation variable shape,
// which is the contract with lib/api — delete takes { id, ticketRef }, the
// other two take it as a named field alongside the request body.
describe('TeamsAdminPage — the optional ticket reference (A3)', () => {
  it('sends a typed reference when creating a team', () => {
    createMutate.mockReset();
    renderPage();

    fireEvent.click(screen.getByTestId('team-create-button'));
    fireEvent.change(screen.getByTestId('team-create-name'), { target: { value: 'DevOps' } });
    fireEvent.change(screen.getByTestId('team-create-ticket-ref'), { target: { value: 'ORG-3' } });
    fireEvent.click(screen.getByTestId('team-create-submit'));

    expect(createMutate).toHaveBeenCalledTimes(1);
    expect(createMutate.mock.calls[0][0].ticketRef).toBe('ORG-3');
  });

  // The negative half: no reference typed means no reference sent, so the
  // request is byte-for-byte the one this surface always made.
  it('sends no reference when creating without one', () => {
    createMutate.mockReset();
    renderPage();

    fireEvent.click(screen.getByTestId('team-create-button'));
    fireEvent.change(screen.getByTestId('team-create-name'), { target: { value: 'DevOps' } });
    expect(screen.getByTestId('team-create-submit')).not.toBeDisabled();
    fireEvent.click(screen.getByTestId('team-create-submit'));

    expect(createMutate).toHaveBeenCalledTimes(1);
    expect(createMutate.mock.calls[0][0].ticketRef).toBeUndefined();
  });

  it('sends a typed reference when renaming a team', () => {
    updateMutate.mockReset();
    renderPage();

    fireEvent.click(screen.getByTestId('team-edit-button'));
    fireEvent.change(screen.getByTestId('team-edit-name'), { target: { value: 'Platform Eng' } });
    fireEvent.change(screen.getByTestId('team-edit-ticket-ref'), { target: { value: 'ORG-4' } });
    fireEvent.click(screen.getByTestId('team-edit-submit'));

    expect(updateMutate).toHaveBeenCalledTimes(1);
    expect(updateMutate.mock.calls[0][0]).toMatchObject({
      teamId: 't1', name: 'Platform Eng', ticketRef: 'ORG-4',
    });
  });

  // The reference field belongs to the SECOND step of the inline delete
  // confirmation: it appears with "Confirm delete" and never gates it.
  it('reveals the reference field only while confirming a delete, and sends it', () => {
    deleteMutate.mockReset();
    renderPage();

    expect(screen.queryByTestId('team-delete-ticket-ref')).toBeNull();
    fireEvent.click(screen.getByTestId('team-delete-button'));
    expect(screen.getByTestId('team-delete-ticket-ref')).toBeInTheDocument();
    expect(screen.getByTestId('team-delete-confirm')).not.toBeDisabled();

    fireEvent.change(screen.getByTestId('team-delete-ticket-ref'), { target: { value: 'ORG-9' } });
    fireEvent.click(screen.getByTestId('team-delete-confirm'));

    expect(deleteMutate).toHaveBeenCalledTimes(1);
    expect(deleteMutate.mock.calls[0][0]).toEqual({ id: 't1', ticketRef: 'ORG-9' });
    // Confirming ends the second step, so the field goes away with it.
    expect(screen.queryByTestId('team-delete-ticket-ref')).toBeNull();
  });
});

// B5: under AZIMUTHAL_TICKET_REF_REQUIRED the dialogs pre-empt the server's
// 400 instead of letting the operator discover the requirement from a failed
// request. Both directions matter: the required=false half is what proves the
// shipped behaviour of every deployment that never set the flag is untouched.
describe('TeamsAdminPage — required mode gates the submits (B5)', () => {
  afterEach(() => {
    deployment.ticketRefRequired = false;
  });

  it('leaves create enabled with an empty reference when the deployment does not require one', () => {
    deployment.ticketRefRequired = false;
    renderPage();

    fireEvent.click(screen.getByTestId('team-create-button'));
    fireEvent.change(screen.getByTestId('team-create-name'), { target: { value: 'DevOps' } });

    expect(screen.getByTestId('team-create-submit')).toBeEnabled();
  });

  it('disables create until a reference is typed when the deployment requires one', () => {
    deployment.ticketRefRequired = true;
    createMutate.mockReset();
    renderPage();

    fireEvent.click(screen.getByTestId('team-create-button'));
    fireEvent.change(screen.getByTestId('team-create-name'), { target: { value: 'DevOps' } });
    expect(screen.getByTestId('team-create-submit')).toBeDisabled();

    // Whitespace is not a reference: the server trims before deciding.
    fireEvent.change(screen.getByTestId('team-create-ticket-ref'), { target: { value: '   ' } });
    expect(screen.getByTestId('team-create-submit')).toBeDisabled();

    fireEvent.change(screen.getByTestId('team-create-ticket-ref'), { target: { value: 'CHG-1' } });
    expect(screen.getByTestId('team-create-submit')).toBeEnabled();

    fireEvent.click(screen.getByTestId('team-create-submit'));
    expect(createMutate).toHaveBeenCalledTimes(1);
    expect(createMutate.mock.calls[0][0].ticketRef).toBe('CHG-1');
  });

  it('marks the create field required so the label stops saying optional', () => {
    deployment.ticketRefRequired = true;
    renderPage();

    fireEvent.click(screen.getByTestId('team-create-button'));

    expect(screen.getByTestId('team-create-ticket-ref')).toBeRequired();
  });
});
