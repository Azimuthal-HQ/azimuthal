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

const resendMutate = vi.fn();

vi.mock('../../../lib/api', () => ({
  friendlyErrorMessage: (_e: unknown, f: string) => f,
  useOrgPeople: () => ({ data: [], isLoading: false, error: null }),
  useInvites: () => ({ data: [INVITE_A, INVITE_B], isLoading: false, error: null }),
  useTeams: () => ({ data: [] }),
  useCreateInvites: () => ({ mutate: vi.fn(), isPending: false }),
  usePersonLifecycle: () => ({ mutate: vi.fn(), isPending: false }),
  useRemovePerson: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdatePerson: () => ({ mutate: vi.fn(), isPending: false }),
  useRevokeInvite: () => ({ mutate: vi.fn(), isPending: false }),
  useResendInvite: () => ({ mutate: resendMutate, isPending: false }),
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
