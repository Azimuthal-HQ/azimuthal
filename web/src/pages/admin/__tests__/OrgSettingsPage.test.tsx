import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { OrgSettingsPage } from '../OrgSettingsPage';

// S9: org settings live in the admin panel. The slug is display-only (it is
// not writable via any API and no URL uses it), and Save sends only name +
// description.

const updateMutateAsync = vi.fn().mockResolvedValue({});

vi.mock('../../../lib/api', () => {
  // Stable reference — React Query returns a stable object until data changes;
  // a fresh literal each call would re-fire the populate effect every render.
  const ORG = { id: 'org-1', name: 'Acme', slug: 'acme', description: 'We make things' };
  return {
    friendlyErrorMessage: (_e: unknown, f: string) => f,
    useOrganization: () => ({ data: ORG }),
    useUpdateOrganization: () => ({ mutateAsync: updateMutateAsync, isPending: false, error: null }),
  };
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
});
