import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CustomFieldsAdminPage } from '../CustomFieldsAdminPage';

const createMutate = vi.fn();
const updateMutate = vi.fn();
const deleteMutate = vi.fn();
const setScopeMutate = vi.fn();
const removeScopeMutate = vi.fn();
const setScopeMutateAsync = vi.fn();
const removeScopeMutateAsync = vi.fn();
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
const defaultSpacesState = {
  data: [
    { id: 's-vec', name: 'Engineering', slug: 'eng', type: 'vector' },
    { id: 's-desk', name: 'Helpdesk', slug: 'desk', type: 'beacon' },
  ],
  isLoading: false,
  isError: false,
  error: null,
};
const defaultScopesState = {
  data: [{ field_id: 'f1', space_id: 's-vec', entity_type: 'project_item', required: true, position: 1 }],
  isLoading: false,
  isError: false,
  error: null,
};
const useSpacesMock = vi.fn(() => defaultSpacesState);
const useFieldScopesMock = vi.fn(() => defaultScopesState);

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
  useSetFieldScope: () => ({ mutate: setScopeMutate, mutateAsync: setScopeMutateAsync, isPending: false }),
  useRemoveFieldScope: () => ({ mutate: removeScopeMutate, mutateAsync: removeScopeMutateAsync, isPending: false }),
  useFormFieldScopes: () => useFormFieldScopesMock(),
  useReorderFormFields: () => ({ mutate: reorderMutate, isPending: false }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

afterEach(() => {
  createMutate.mockReset();
  deleteMutate.mockReset();
  setScopeMutate.mockReset();
  removeScopeMutate.mockReset();
  setScopeMutateAsync.mockReset();
  removeScopeMutateAsync.mockReset();
  reorderMutate.mockReset();
  useSpacesMock.mockImplementation(() => defaultSpacesState);
  useFieldScopesMock.mockImplementation(() => defaultScopesState);
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

describe('bulk attach/detach', () => {
  // Three vector spaces, one already attached (required): the targeting rule
  // under test is that bulk operations touch only spaces whose state has to
  // change — that is what keeps required flags safe and re-runs convergent.
  const threeVector = {
    ...defaultSpacesState,
    data: [
      { id: 's1', name: 'Alpha', slug: 'a', type: 'vector' },
      { id: 's2', name: 'Beta', slug: 'b', type: 'vector' },
      { id: 's3', name: 'Gamma', slug: 'c', type: 'vector' },
    ],
  };
  const attachedTo = (...spaceIds: string[]) => ({
    ...defaultScopesState,
    data: spaceIds.map((id) => ({
      field_id: 'f1', space_id: id, entity_type: 'project_item', required: true, position: 1,
    })),
  });

  it('attach-all PUTs only the spaces not yet attached, never re-flagging an existing one', async () => {
    useSpacesMock.mockImplementation(() => threeVector);
    useFieldScopesMock.mockImplementation(() => attachedTo('s1'));
    setScopeMutateAsync.mockResolvedValue(undefined);
    renderPage();
    fireEvent.click(screen.getByLabelText('Attachments for Points'));

    fireEvent.click(screen.getByTestId('bulk-attach-project_item'));

    await waitFor(() => expect(setScopeMutateAsync).toHaveBeenCalledTimes(2));
    const targeted = setScopeMutateAsync.mock.calls.map((c) => (c[0] as { spaceId: string }).spaceId).sort();
    expect(targeted).toEqual(['s2', 's3']);
    // s1 is attached and required — a re-PUT would reset the flag to false.
    expect(targeted).not.toContain('s1');
    expect(screen.queryByTestId('bulk-scope-failure')).toBeNull();
  });

  it('names the spaces a partial failure left behind, and a re-run retries only those', async () => {
    useSpacesMock.mockImplementation(() => threeVector);
    useFieldScopesMock.mockImplementation(() => attachedTo('s1'));
    setScopeMutateAsync.mockImplementation(({ spaceId }: { spaceId: string }) =>
      spaceId === 's2' ? Promise.reject(new Error('boom')) : Promise.resolve(undefined),
    );
    const view = renderPage();
    fireEvent.click(screen.getByLabelText('Attachments for Points'));

    fireEvent.click(screen.getByTestId('bulk-attach-project_item'));

    const failure = await screen.findByTestId('bulk-scope-failure');
    expect(failure).toHaveTextContent('Beta');
    expect(failure).not.toHaveTextContent('Gamma');

    // Convergence: the succeeded space is now attached (the mutation's
    // invalidation refetches the scope list — modelled here by swapping the
    // mock and re-rendering), so a re-run targets exactly the failed one.
    // The loop is idempotent because it re-derives its targets from current
    // state, not because PUT is retried blindly.
    useFieldScopesMock.mockImplementation(() => attachedTo('s1', 's3'));
    setScopeMutateAsync.mockClear().mockResolvedValue(undefined);
    view.rerender(
      <MemoryRouter>
        <CustomFieldsAdminPage />
      </MemoryRouter>,
    );
    fireEvent.click(screen.getByTestId('bulk-attach-project_item'));

    await waitFor(() => expect(setScopeMutateAsync).toHaveBeenCalledTimes(1));
    expect((setScopeMutateAsync.mock.calls[0][0] as { spaceId: string }).spaceId).toBe('s2');
  });

  it('detach-all DELETEs only the spaces that hold an attachment', async () => {
    useSpacesMock.mockImplementation(() => threeVector);
    useFieldScopesMock.mockImplementation(() => attachedTo('s1', 's3'));
    removeScopeMutateAsync.mockResolvedValue(undefined);
    renderPage();
    fireEvent.click(screen.getByLabelText('Attachments for Points'));

    fireEvent.click(screen.getByTestId('bulk-detach-project_item'));

    await waitFor(() => expect(removeScopeMutateAsync).toHaveBeenCalledTimes(2));
    const targeted = removeScopeMutateAsync.mock.calls.map((c) => (c[0] as { spaceId: string }).spaceId).sort();
    // s2 holds no attachment — a DELETE there would 404 and a re-run after a
    // partial failure would report already-detached spaces as new failures.
    expect(targeted).toEqual(['s1', 's3']);
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
