import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CustomFieldsAdminPage } from '../CustomFieldsAdminPage';

const createMutate = vi.fn();
const updateMutate = vi.fn();
const deleteMutate = vi.fn();

const useCustomFieldsMock = vi.fn(() => ({
  data: [
    { id: 'f1', org_id: 'o1', slug: 'points', name: 'Points', field_type: 'number', options: [], position: 1, archived_at: null, created_at: '', updated_at: '' },
    { id: 'f2', org_id: 'o1', slug: 'tier', name: 'Tier', field_type: 'single_select', options: ['gold', 'silver'], position: 2, archived_at: null, created_at: '', updated_at: '' },
  ],
  isLoading: false,
  isError: false,
  error: null,
}));

vi.mock('../../../lib/auth', () => ({ useAuth: () => ({ user: { orgId: 'o1' } }) }));

vi.mock('../../../lib/api', () => ({
  useCustomFields: () => useCustomFieldsMock(),
  useCreateCustomField: () => ({ mutate: createMutate, isPending: false }),
  useUpdateCustomField: () => ({ mutate: updateMutate, isPending: false }),
  useDeleteCustomField: () => ({ mutate: deleteMutate, isPending: false }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

afterEach(() => {
  createMutate.mockReset();
  deleteMutate.mockReset();
});

function renderPage() {
  return render(
    <MemoryRouter>
      <CustomFieldsAdminPage />
    </MemoryRouter>,
  );
}

describe('CustomFieldsAdminPage', () => {
  it('lists fields with their types and options', () => {
    renderPage();
    expect(screen.getByText('Points')).toBeInTheDocument();
    expect(screen.getByText('Number')).toBeInTheDocument();
    expect(screen.getByText('Single select')).toBeInTheDocument();
    expect(screen.getByText('gold, silver')).toBeInTheDocument();
  });

  it('shows an options input only for single-select and creates a field', () => {
    renderPage();
    fireEvent.click(screen.getByTestId('custom-field-create-button'));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Story points' } });
    // Text type → no options input.
    expect(screen.queryByLabelText('Options (comma-separated)')).toBeNull();
    // Switch to single_select → options input appears.
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'single_select' } });
    fireEvent.change(screen.getByLabelText('Options (comma-separated)'), { target: { value: 'a, b' } });
    fireEvent.click(screen.getByTestId('custom-field-create-submit'));
    expect(createMutate).toHaveBeenCalledTimes(1);
    expect(createMutate.mock.calls[0][0]).toEqual({ name: 'Story points', field_type: 'single_select', options: ['a', 'b'] });
  });
});
