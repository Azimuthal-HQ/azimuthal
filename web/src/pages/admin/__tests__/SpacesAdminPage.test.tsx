import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SpacesAdminPage } from '../SpacesAdminPage';

// The admin panel is the ONE surface that edits space visibility
// (set_visibility is org-admin-only; the card left space settings in the
// interior restyle). These tests are the positive half of that move: they
// fail if the radio-card visibility control disappears from the edit dialog
// or its selection stops reaching the update payload.

const updateMutate = vi.fn();
const deleteMutate = vi.fn();

const SPACE = {
  id: 'space-1',
  org_id: 'org-1',
  name: 'Support',
  slug: 'support',
  key: 'SUP',
  type: 'beacon' as const,
  description: null,
  icon: null,
  is_private: false,
  owner_team_id: 't1',
  visibility: 'discoverable' as const,
  created_at: '',
  updated_at: '',
};

// vi.mock factories are hoisted above module-scope declarations, so the
// required-mode switch these tests flip has to be hoisted with them.
// vi.hoisted is the sanctioned way; a plain `let` here throws
// "Cannot access before initialization" inside the factory.
const deployment = vi.hoisted(() => ({ ticketRefRequired: false }));

vi.mock('../../../lib/api', () => ({
  useSpaces: vi.fn(() => ({ data: [SPACE], isLoading: false, error: null })),
  useTeams: vi.fn(() => ({ data: [{ id: 't1', name: 'Default', slug: 'default', is_default: true }], isLoading: false })),
  useUpdateSpace: vi.fn(() => ({ mutate: updateMutate, isPending: false, error: null })),
  useDeleteSpace: vi.fn(() => ({ mutate: deleteMutate, isPending: false, error: null })),
  useSpaceContentsSummary: vi.fn(() => ({ data: { tickets: 0, pages: 0, items: 0 }, isLoading: false })),
  friendlyErrorMessage: vi.fn((_err: unknown, fallback: string) => fallback),
  // A3: the edit and delete dialogs now carry a TicketRefField.
  useTicketRefSuggestions: vi.fn(() => ({ data: [], isLoading: false, error: null })),
  useTicketRefRequired: () => deployment.ticketRefRequired,
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: vi.fn(() => ({
    user: { id: 'u1', email: 'admin@example.com', orgId: 'org-1', role: 'admin' },
    isAuthenticated: true,
    login: vi.fn(),
    logout: vi.fn(),
  })),
}));

describe('SpacesAdminPage edit dialog — the visibility edit surface', () => {
  beforeEach(() => {
    updateMutate.mockReset();
    deleteMutate.mockReset();
  });

  function openEditDialog() {
    render(<SpacesAdminPage />);
    fireEvent.click(screen.getByTestId('admin-space-edit-support'));
    expect(screen.getByTestId('admin-space-edit-dialog')).toBeInTheDocument();
  }

  it('renders the radio-card visibility group with the current value selected', () => {
    openEditDialog();

    const group = screen.getByTestId('admin-space-visibility');
    expect(group).toBeInTheDocument();
    expect(screen.getAllByRole('radio')).toHaveLength(3);
    expect(screen.getByRole('radio', { name: /Discoverable/ })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('radio', { name: /Hidden/ })).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByRole('radio', { name: /Org/ })).toHaveAttribute('aria-checked', 'false');
  });

  it('sends the newly selected visibility in the update payload on Save', () => {
    openEditDialog();

    fireEvent.click(screen.getByRole('radio', { name: /Org/ }));
    expect(screen.getByRole('radio', { name: /Org/ })).toHaveAttribute('aria-checked', 'true');

    fireEvent.click(screen.getByTestId('admin-space-save'));

    expect(updateMutate).toHaveBeenCalledTimes(1);
    const payload = updateMutate.mock.calls[0][0];
    expect(payload.visibility).toBe('org');
    // PUT semantics: fields the dialog does not edit are echoed, not dropped.
    expect(payload.name).toBe('Support');
    expect(payload.key).toBe('SUP');
  });

  // A3: the space edit and delete surfaces can carry the operator's ticket
  // reference. Before A3 both sent none. The shapes differ and both matter:
  // update takes it inside the request object, delete takes { id, ticketRef }.
  it('sends a typed reference with the space update', () => {
    openEditDialog();

    fireEvent.change(screen.getByTestId('admin-space-ticket-ref'), { target: { value: 'OPS-21' } });
    fireEvent.click(screen.getByTestId('admin-space-save'));

    expect(updateMutate).toHaveBeenCalledTimes(1);
    expect(updateMutate.mock.calls[0][0].ticketRef).toBe('OPS-21');
  });

  // The negative half: nothing typed means nothing sent.
  it('sends no reference when the operator types none', () => {
    openEditDialog();

    expect(screen.getByTestId('admin-space-save')).not.toBeDisabled();
    fireEvent.click(screen.getByTestId('admin-space-save'));

    expect(updateMutate).toHaveBeenCalledTimes(1);
    expect(updateMutate.mock.calls[0][0].ticketRef).toBeUndefined();
  });

  it('sends a typed reference with the space deletion as { id, ticketRef }', () => {
    render(<SpacesAdminPage />);
    fireEvent.click(screen.getByTestId('admin-space-delete-support'));

    fireEvent.change(screen.getByTestId('admin-space-delete-ticket-ref'), {
      target: { value: 'OPS-22' },
    });
    // Never a gate on the destructive confirmation.
    expect(screen.getByTestId('admin-space-delete-confirm')).not.toBeDisabled();
    fireEvent.click(screen.getByTestId('admin-space-delete-confirm'));

    expect(deleteMutate).toHaveBeenCalledTimes(1);
    expect(deleteMutate.mock.calls[0][0]).toEqual({ id: 'space-1', ticketRef: 'OPS-22' });
  });
});
