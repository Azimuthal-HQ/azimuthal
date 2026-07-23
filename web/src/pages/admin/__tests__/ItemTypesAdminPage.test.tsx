import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ItemTypesAdminPage } from '../ItemTypesAdminPage';

const createMutate = vi.fn();
const updateMutate = vi.fn();
const deleteMutate = vi.fn();

const useItemTypesMock = vi.fn(() => ({
  data: [
    { id: 't1', org_id: 'o1', slug: 'task', name: 'Task', position: 1, archived_at: null, created_at: '', updated_at: '' },
    { id: 't2', org_id: 'o1', slug: 'bug', name: 'Bug', position: 3, archived_at: '2026-01-01T00:00:00Z', created_at: '', updated_at: '' },
  ],
  isLoading: false,
  isError: false,
  error: null,
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { orgId: 'o1' } }),
}));

vi.mock('../../../lib/api', () => ({
  useItemTypes: () => useItemTypesMock(),
  useCreateItemType: () => ({ mutate: createMutate, isPending: false }),
  useUpdateItemType: () => ({ mutate: updateMutate, isPending: false }),
  useDeleteItemType: () => ({ mutate: deleteMutate, isPending: false }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

function renderPage() {
  return render(
    <MemoryRouter>
      <ItemTypesAdminPage />
    </MemoryRouter>,
  );
}

afterEach(() => {
  createMutate.mockReset();
  updateMutate.mockReset();
  deleteMutate.mockReset();
});

describe('ItemTypesAdminPage', () => {
  it('lists types with their slugs and flags archived ones', () => {
    renderPage();
    expect(screen.getByText('Task')).toBeInTheDocument();
    expect(screen.getByText('task')).toBeInTheDocument();
    expect(screen.getByText('Bug')).toBeInTheDocument();
    // The archived type carries an "Archived" badge.
    expect(screen.getByText('Archived')).toBeInTheDocument();
  });

  it('creates a type through the dialog', () => {
    renderPage();
    fireEvent.click(screen.getByTestId('item-type-create-button'));
    const input = screen.getByLabelText('Name');
    fireEvent.change(input, { target: { value: 'Spike' } });
    fireEvent.click(screen.getByTestId('item-type-create-submit'));
    expect(createMutate).toHaveBeenCalledTimes(1);
    expect(createMutate.mock.calls[0][0]).toEqual({ name: 'Spike' });
  });

  it('requires a two-step confirm before deleting', () => {
    renderPage();
    // First delete click reveals the confirm button; the mutation is not called yet.
    fireEvent.click(screen.getAllByLabelText('Delete')[0]);
    expect(deleteMutate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('item-type-confirm-delete'));
    expect(deleteMutate).toHaveBeenCalledTimes(1);
  });
});
