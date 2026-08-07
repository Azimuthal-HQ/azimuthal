import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CustomFieldsAdminPage } from '../CustomFieldsAdminPage';

const createMutate = vi.fn();
const updateMutate = vi.fn();
const deleteMutate = vi.fn();
const setScopeMutate = vi.fn();
const removeScopeMutate = vi.fn();
const reorderMutate = vi.fn();

const useCustomFieldsMock = vi.fn(() => ({
  data: [
    { id: 'f1', org_id: 'o1', slug: 'points', name: 'Points', field_type: 'number', options: [], position: 1, archived_at: null, created_at: '', updated_at: '' },
    { id: 'f2', org_id: 'o1', slug: 'tier', name: 'Tier', field_type: 'single_select', options: ['gold', 'silver'], position: 2, archived_at: null, created_at: '', updated_at: '' },
  ],
  isLoading: false,
  isError: false,
  error: null,
}));

// One vector space and one beacon space, so the panel shows both form groups;
// f1 is attached to the vector space's item form, required.
const useSpacesMock = vi.fn(() => ({
  data: [
    { id: 's-vec', name: 'Engineering', slug: 'eng', type: 'vector' },
    { id: 's-desk', name: 'Helpdesk', slug: 'desk', type: 'beacon' },
  ],
  isLoading: false,
  isError: false,
  error: null,
}));
const useFieldScopesMock = vi.fn(() => ({
  data: [{ field_id: 'f1', space_id: 's-vec', entity_type: 'project_item', required: true, position: 1 }],
  isLoading: false,
  isError: false,
  error: null,
}));

// One form's rows for the ordering panel: the vector space's item form
// carries both fields, f1 first and required.
const useFormFieldScopesMock = vi.fn(() => ({
  data: [
    { field_id: 'f1', space_id: 's-vec', entity_type: 'project_item', required: true, position: 1 },
    { field_id: 'f2', space_id: 's-vec', entity_type: 'project_item', required: false, position: 2 },
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
  useSpaces: () => useSpacesMock(),
  useFieldScopes: () => useFieldScopesMock(),
  useSetFieldScope: () => ({ mutate: setScopeMutate, isPending: false }),
  useRemoveFieldScope: () => ({ mutate: removeScopeMutate, isPending: false }),
  useFormFieldScopes: () => useFormFieldScopesMock(),
  useReorderFormFields: () => ({ mutate: reorderMutate, isPending: false }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

afterEach(() => {
  createMutate.mockReset();
  deleteMutate.mockReset();
  setScopeMutate.mockReset();
  removeScopeMutate.mockReset();
  reorderMutate.mockReset();
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

  it('expands a field into its attachments with the required flag per scope', () => {
    renderPage();
    // Collapsed rows fetch no scopes and render no panel.
    expect(screen.queryByTestId('field-scopes-panel')).toBeNull();

    fireEvent.click(screen.getByLabelText('Attachments for Points'));
    expect(screen.getByTestId('field-scopes-panel')).toBeInTheDocument();

    // The vector space's item form is attached and required; the beacon
    // space's ticket form is not attached, so it has no required toggle.
    const attach = screen.getByTestId('scope-attach-s-vec-project_item') as HTMLInputElement;
    expect(attach.checked).toBe(true);
    const required = screen.getByTestId('scope-required-s-vec-project_item') as HTMLInputElement;
    expect(required.checked).toBe(true);
    const desk = screen.getByTestId('scope-attach-s-desk-ticket') as HTMLInputElement;
    expect(desk.checked).toBe(false);
    expect(screen.queryByTestId('scope-required-s-desk-ticket')).toBeNull();
  });

  it('attaches, re-flags and detaches through the scope mutations', () => {
    renderPage();
    fireEvent.click(screen.getByLabelText('Attachments for Points'));

    // Ticking an unattached space attaches it, not-required by default.
    fireEvent.click(screen.getByTestId('scope-attach-s-desk-ticket'));
    expect(setScopeMutate).toHaveBeenCalledTimes(1);
    expect(setScopeMutate.mock.calls[0][0]).toEqual({ spaceId: 's-desk', entityType: 'ticket', required: false });

    // Toggling required re-puts the same attachment with the flag flipped.
    fireEvent.click(screen.getByTestId('scope-required-s-vec-project_item'));
    expect(setScopeMutate).toHaveBeenCalledTimes(2);
    expect(setScopeMutate.mock.calls[1][0]).toEqual({ spaceId: 's-vec', entityType: 'project_item', required: false });

    // Unticking an attached space detaches it.
    fireEvent.click(screen.getByTestId('scope-attach-s-vec-project_item'));
    expect(removeScopeMutate).toHaveBeenCalledTimes(1);
    expect(removeScopeMutate.mock.calls[0][0]).toEqual({ spaceId: 's-vec', entityType: 'project_item' });
  });
});

describe('FormOrderPanel', () => {
  it('shows a form picker and no rows until a form is chosen', () => {
    renderPage();
    expect(screen.getByTestId('form-order-panel')).toBeInTheDocument();
    expect(screen.queryAllByTestId('form-order-row')).toHaveLength(0);
  });

  it('renders the chosen form in scope order with names and required flags', () => {
    renderPage();
    fireEvent.change(screen.getByTestId('form-order-space'), { target: { value: 's-vec' } });

    const rows = screen.getAllByTestId('form-order-row');
    expect(rows).toHaveLength(2);
    // Names resolved from the definitions list, in the order the form
    // reports, with the required badge on the row that carries the flag.
    expect(rows[0]).toHaveTextContent('Points');
    expect(rows[0]).toHaveTextContent('Required');
    expect(rows[1]).toHaveTextContent('Tier');
    expect(rows[1]).not.toHaveTextContent('Required');
  });

  it('submits the whole permutation on a move, not a delta', () => {
    renderPage();
    fireEvent.change(screen.getByTestId('form-order-space'), { target: { value: 's-vec' } });

    fireEvent.click(screen.getByTestId('form-order-down-f1'));
    expect(reorderMutate).toHaveBeenCalledTimes(1);
    // The route takes a permutation of the form; the panel must send every
    // field, in the new order.
    expect(reorderMutate.mock.calls[0][0]).toEqual(['f2', 'f1']);
  });

  it('pins the ends: first row cannot move up, last cannot move down', () => {
    renderPage();
    fireEvent.change(screen.getByTestId('form-order-space'), { target: { value: 's-vec' } });

    expect(screen.getByTestId('form-order-up-f1')).toBeDisabled();
    expect(screen.getByTestId('form-order-down-f2')).toBeDisabled();
    fireEvent.click(screen.getByTestId('form-order-up-f1'));
    expect(reorderMutate).not.toHaveBeenCalled();
  });
});
