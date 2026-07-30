import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { TopBar } from '../TopBar';

// S3: the Create button is module-contextual. Its primary action creates the
// current module's entity in the current space (Beacon ticket / Codex page /
// Vector item), falling back to "new space" when no space is in context.
// Before S3 it navigated to /?create=space unconditionally and carried no
// data-testid.

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return { ...actual, useNavigate: () => mockNavigate };
});
vi.mock('../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'u1', email: 'a@b.c', orgId: 'org-1', role: 'member' }, logout: vi.fn() }),
}));
vi.mock('../../lib/api', () => ({
  useOrganization: () => ({ data: { id: 'org-1', name: 'Org', caller_is_admin: false } }),
  useNotifications: () => ({ data: { notifications: [], unread_count: 0 } }),
  useMarkNotificationRead: () => ({ mutate: vi.fn() }),
  useMarkAllNotificationsRead: () => ({ mutate: vi.fn() }),
  // The top bar's search control (P6) lives in the tree these tests render, so
  // its data hook has to exist on the mock. Idle by default — this file is
  // about the contextual Create button, and a launcher that never opens issues
  // no request anyway; search behaviour has its own tests.
  useSearch: () => ({ data: undefined, isLoading: false, isError: false }),
  splitSnippet: () => [],
}));
vi.mock('../ShellUIContext', () => ({
  useShellUI: () => ({ mobileNavOpen: false, setMobileNavOpen: vi.fn() }),
}));

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <TopBar />
    </MemoryRouter>,
  );
}

describe('TopBar contextual Create', () => {
  beforeEach(() => mockNavigate.mockReset());

  it('creates a ticket from a Beacon space', () => {
    renderAt('/beacon/space-1/tickets');
    fireEvent.click(screen.getByTestId('topbar-create'));
    expect(mockNavigate).toHaveBeenCalledWith('/beacon/space-1/tickets?create=ticket');
  });

  it('creates a page from a Codex space', () => {
    renderAt('/codex/space-2/pages/p1');
    fireEvent.click(screen.getByTestId('topbar-create'));
    expect(mockNavigate).toHaveBeenCalledWith('/codex/space-2?create=page');
  });

  it('creates an item from a Vector space (even on the board sub-route)', () => {
    renderAt('/vector/space-9/board');
    fireEvent.click(screen.getByTestId('topbar-create'));
    expect(mockNavigate).toHaveBeenCalledWith('/vector/space-9/backlog?create=item');
  });

  it('falls back to creating a space on Home (no space in context)', () => {
    renderAt('/');
    fireEvent.click(screen.getByTestId('topbar-create'));
    expect(mockNavigate).toHaveBeenCalledWith('/?create=space');
  });

  it('does not treat a non-module route as a module context', () => {
    renderAt('/admin/people');
    fireEvent.click(screen.getByTestId('topbar-create'));
    expect(mockNavigate).toHaveBeenCalledWith('/?create=space');
  });
});
