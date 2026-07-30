import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { OrgSettingsPage } from '../OrgSettingsPage';

// S9: org settings live in the admin panel. The slug is display-only (it is
// not writable via any API and no URL uses it), and Save sends only name +
// description.

const updateMutateAsync = vi.fn().mockResolvedValue({});

// The reference is STABLE until something changes it — React Query returns a
// stable object until the data changes, and a fresh literal per call would
// re-fire the populate effect on every render. Reassigning it and re-rendering
// is a refetch that brought new data, which is the only thing that re-fires the
// effect in production and therefore the only thing the guard tests can use.
const INITIAL_ORG = { id: 'org-1', name: 'Acme', slug: 'acme', description: 'We make things' };
let org = INITIAL_ORG;

function refetched(changes: Partial<typeof INITIAL_ORG>) {
  org = { ...org, ...changes };
}

vi.mock('../../../lib/api', () => ({
  friendlyErrorMessage: (_e: unknown, f: string) => f,
  useOrganization: () => ({ data: org }),
  useUpdateOrganization: () => ({ mutateAsync: updateMutateAsync, isPending: false, error: null }),
}));

afterEach(() => {
  org = INITIAL_ORG;
  updateMutateAsync.mockClear();
});

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'u1', orgId: 'org-1', role: 'admin' } }),
}));

function renderPage() {
  return render(
    <MemoryRouter>
      <OrgSettingsPage />
    </MemoryRouter>,
  );
}

describe('OrgSettingsPage', () => {
  it('renders the slug as a display-only field', () => {
    renderPage();
    const slug = screen.getByTestId('admin-org-slug') as HTMLInputElement;
    expect(slug.value).toBe('acme');
    expect(slug).toBeDisabled();
  });

  it('saves only name and description (never slug)', async () => {
    renderPage();
    fireEvent.change(screen.getByTestId('admin-org-name'), { target: { value: 'Acme Corp' } });
    fireEvent.click(screen.getByTestId('admin-org-save'));

    await waitFor(() => expect(updateMutateAsync).toHaveBeenCalledTimes(1));
    const payload = updateMutateAsync.mock.calls[0][0];
    expect(payload).toEqual({ name: 'Acme Corp', description: 'We make things' });
    expect(payload).not.toHaveProperty('slug');
  });

  // The same hazard CustomFieldsSection carries, found by auditing the sibling
  // admin sections: this form copies fetched data into editable state and had
  // no dirty flag, so a refetch landing mid-edit discarded what was typed. The
  // effect depends on the whole `org` object, so a change to EITHER field
  // re-seeded BOTH — which is what this asserts.
  it('keeps an in-progress edit when a refetch changes the organisation', () => {
    const { rerender } = renderPage();
    fireEvent.change(screen.getByTestId('admin-org-name'), { target: { value: 'Acme Corp' } });

    refetched({ description: 'Edited by somebody else' });
    rerender(<MemoryRouter><OrgSettingsPage /></MemoryRouter>);

    expect((screen.getByTestId('admin-org-name') as HTMLInputElement).value).toBe('Acme Corp');
  });

  // And a form nobody is editing still follows the server, which is what the
  // effect is for in the first place.
  it('picks up a server change when there is no edit in progress', () => {
    const { rerender } = renderPage();
    expect((screen.getByTestId('admin-org-name') as HTMLInputElement).value).toBe('Acme');

    refetched({ name: 'Acme Holdings' });
    rerender(<MemoryRouter><OrgSettingsPage /></MemoryRouter>);

    expect((screen.getByTestId('admin-org-name') as HTMLInputElement).value).toBe('Acme Holdings');
  });

  // The flag must not be sticky: once the server holds what is on screen the
  // edit is over, or one keystroke would freeze the form for the session.
  it('follows the server again once the save has landed', async () => {
    const { rerender } = renderPage();
    fireEvent.change(screen.getByTestId('admin-org-name'), { target: { value: 'Acme Corp' } });
    fireEvent.click(screen.getByTestId('admin-org-save'));
    await waitFor(() => expect(updateMutateAsync).toHaveBeenCalledTimes(1));

    refetched({ name: 'Acme Corp' }); // the invalidate's refetch lands
    rerender(<MemoryRouter><OrgSettingsPage /></MemoryRouter>);

    refetched({ name: 'Acme Group' }); // and a later change from anywhere
    rerender(<MemoryRouter><OrgSettingsPage /></MemoryRouter>);

    expect((screen.getByTestId('admin-org-name') as HTMLInputElement).value).toBe('Acme Group');
  });
});
