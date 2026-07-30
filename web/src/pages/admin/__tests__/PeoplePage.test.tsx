import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { PeoplePage } from '../PeoplePage';

// S6: resending a link-mode invite re-surfaces the fresh one-time link. The
// backend already rotated the token, invalidated the prior link, and wrote an
// audit event since P2.5; this covers the residual UX fix — the new link is
// shown INLINE under the row that was resent (previously it rendered once at
// the bottom of the card, easy to miss with several pending invites).

const INVITE_A = {
  id: 'inv-a', email: 'a@example.com', org_role: 'member', team_id: null,
  invited_by: 'u1', expires_at: '2026-08-01T00:00:00Z', created_at: '2026-07-01T00:00:00Z', expired: false,
};
const INVITE_B = {
  id: 'inv-b', email: 'b@example.com', org_role: 'member', team_id: null,
  invited_by: 'u1', expires_at: '2026-08-01T00:00:00Z', created_at: '2026-07-01T00:00:00Z', expired: false,
};

const PERSON = {
  user_id: 'u2', email: 'u2@example.com', display_name: 'Old Name', avatar_url: null,
  org_role: 'member', status: 'active', joined_at: '2026-01-01T00:00:00Z', primary_team_id: null,
};

const resendMutate = vi.fn();
const updatePersonMutate = vi.fn();
const uploadAvatarMutate = vi.fn();
const lifecycleMutate = vi.fn();
const removePersonMutate = vi.fn();
const createInvitesMutate = vi.fn();

// vi.mock factories are hoisted above module-scope declarations, so the
// required-mode switch these tests flip has to be hoisted with them.
// vi.hoisted is the sanctioned way; a plain `let` here throws
// "Cannot access before initialization" inside the factory.
const deployment = vi.hoisted(() => ({ ticketRefRequired: false }));

vi.mock('../../../lib/api', () => ({
  friendlyErrorMessage: (_e: unknown, f: string) => f,
  useOrgPeople: () => ({ data: [PERSON], isLoading: false, error: null }),
  useInvites: () => ({ data: [INVITE_A, INVITE_B], isLoading: false, error: null }),
  useTeams: () => ({ data: [] }),
  useCreateInvites: () => ({ mutate: createInvitesMutate, isPending: false }),
  usePersonLifecycle: () => ({ mutate: lifecycleMutate, isPending: false }),
  useRemovePerson: () => ({ mutate: removePersonMutate, isPending: false }),
  useUpdatePerson: () => ({ mutate: updatePersonMutate, isPending: false }),
  useUploadUserAvatar: () => ({ mutate: uploadAvatarMutate, isPending: false }),
  useRevokeInvite: () => ({ mutate: vi.fn(), isPending: false }),
  useResendInvite: () => ({ mutate: resendMutate, isPending: false }),
  // A3: the invite and confirm dialogs now carry a TicketRefField.
  useTicketRefSuggestions: () => ({ data: [], isLoading: false, error: null }),
  useTicketRefRequired: () => deployment.ticketRefRequired,
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'u1', email: 'admin@example.com', orgId: 'org-1', role: 'admin' } }),
}));

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/admin/people']}>
      <PeoplePage />
    </MemoryRouter>,
  );
}

describe('PeoplePage pending invites — resend re-surfaces the link inline', () => {
  it('shows the fresh link under the resent row, not the other row', () => {
    resendMutate.mockImplementation((id: string, opts?: { onSuccess?: (r: unknown) => void }) =>
      opts?.onSuccess?.({
        ...INVITE_B,
        id,
        invite_url: 'https://app.example.com/invite/fresh-token',
        delivered: false,
      }),
    );

    renderPage();

    // No fresh link before resending.
    expect(screen.queryByTestId('invite-link-b@example.com')).toBeNull();

    fireEvent.click(screen.getByTestId('invite-resend-b@example.com'));

    // The fresh link appears INSIDE the resent row...
    const rowB = screen.getByTestId('invite-row-b@example.com');
    expect(within(rowB).getByTestId('invite-link-b@example.com')).toBeInTheDocument();
    expect(within(rowB).getByTestId('invite-link-b@example.com').textContent).toContain(
      'https://app.example.com/invite/fresh-token',
    );

    // ...and NOT under the other invite's row.
    const rowA = screen.getByTestId('invite-row-a@example.com');
    expect(within(rowA).queryByTestId('invite-link-a@example.com')).toBeNull();
    expect(within(rowA).queryByTestId('invite-link-b@example.com')).toBeNull();
  });
});

describe('PeoplePage — admin edits another member (S8)', () => {
  it('renames a member via the inline display-name editor', () => {
    updatePersonMutate.mockReset();
    render(
      <MemoryRouter initialEntries={['/admin/people']}>
        <PeoplePage />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByTestId('person-name-edit-u2@example.com'));
    fireEvent.change(screen.getByTestId('person-name-input-u2@example.com'), {
      target: { value: 'New Name' },
    });
    fireEvent.click(screen.getByTestId('person-name-save-u2@example.com'));

    expect(updatePersonMutate).toHaveBeenCalledTimes(1);
    expect(updatePersonMutate.mock.calls[0][0]).toEqual({ userId: 'u2', display_name: 'New Name' });
  });

  it('uploads an avatar for a member through the shared upload hook', () => {
    uploadAvatarMutate.mockReset();
    render(
      <MemoryRouter initialEntries={['/admin/people']}>
        <PeoplePage />
      </MemoryRouter>,
    );

    const file = new File(['imgbytes'], 'a.png', { type: 'image/png' });
    fireEvent.change(screen.getByTestId('person-avatar-input-u2@example.com'), {
      target: { files: [file] },
    });

    expect(uploadAvatarMutate).toHaveBeenCalledTimes(1);
    expect(uploadAvatarMutate.mock.calls[0][0].userId).toBe('u2');
    expect(uploadAvatarMutate.mock.calls[0][0].file).toBe(file);
  });
});

// A3: the audited people mutations can now carry the operator's ticket
// reference. Before A3 these call sites sent none and the audit events had
// nothing to join a change-management record on. Each test pins the mutation
// variable, because the shape is the contract with lib/api — a lifecycle call
// takes it as a named field, a removal takes { id, ticketRef }.
describe('PeoplePage — the optional ticket reference (A3)', () => {
  function openConfirm(action: 'deactivate' | 'remove') {
    render(
      <MemoryRouter initialEntries={['/admin/people']}>
        <PeoplePage />
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByTestId('person-actions-u2@example.com'));
    fireEvent.click(screen.getByTestId(`person-${action}-u2@example.com`));
  }

  it('sends a typed reference with a deactivation', () => {
    lifecycleMutate.mockReset();
    openConfirm('deactivate');

    fireEvent.change(screen.getByTestId('person-ticket-ref'), { target: { value: 'SEC-88' } });
    fireEvent.click(screen.getByTestId('person-confirm-action'));

    expect(lifecycleMutate).toHaveBeenCalledTimes(1);
    expect(lifecycleMutate.mock.calls[0][0]).toEqual({
      userId: 'u2', action: 'deactivate', ticketRef: 'SEC-88',
    });
  });

  // The negative half: a surface that collects no reference must send none —
  // lib/api turns an absent ticketRef into exactly the URL it always built.
  // A blank string here would append `?ticket_ref=`, a different request.
  it('sends no reference when the operator types nothing, and never blocks on it', () => {
    lifecycleMutate.mockReset();
    openConfirm('deactivate');

    // The field is present and optional — confirming is not gated on it.
    expect(screen.getByTestId('person-ticket-ref')).toHaveValue('');
    expect(screen.getByTestId('person-confirm-action')).not.toBeDisabled();
    fireEvent.click(screen.getByTestId('person-confirm-action'));

    expect(lifecycleMutate).toHaveBeenCalledTimes(1);
    expect(lifecycleMutate.mock.calls[0][0].ticketRef).toBeUndefined();
  });

  it('sends a typed reference with a removal as { id, ticketRef }', () => {
    removePersonMutate.mockReset();
    openConfirm('remove');

    fireEvent.change(screen.getByTestId('person-ticket-ref'), { target: { value: 'HR-12' } });
    fireEvent.click(screen.getByTestId('person-confirm-action'));

    expect(removePersonMutate).toHaveBeenCalledTimes(1);
    expect(removePersonMutate.mock.calls[0][0]).toEqual({ id: 'u2', ticketRef: 'HR-12' });
  });

  it('sends a typed reference with an invite batch', () => {
    createInvitesMutate.mockReset();
    render(
      <MemoryRouter initialEntries={['/admin/people']}>
        <PeoplePage />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByTestId('people-invite-button'));
    fireEvent.change(screen.getByTestId('invite-emails'), { target: { value: 'new@example.com' } });
    fireEvent.change(screen.getByTestId('invite-ticket-ref'), { target: { value: 'ONB-5' } });
    fireEvent.click(screen.getByTestId('invite-submit'));

    expect(createInvitesMutate).toHaveBeenCalledTimes(1);
    expect(createInvitesMutate.mock.calls[0][0].ticketRef).toBe('ONB-5');
    expect(createInvitesMutate.mock.calls[0][0].emails).toEqual(['new@example.com']);
  });
});
