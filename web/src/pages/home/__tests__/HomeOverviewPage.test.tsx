import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '../../../lib/api';
import { HomeOverviewPage } from '../HomeOverviewPage';

// Partial mock: the hooks are stubbed, but APIError and friendlyErrorMessage
// stay real — the point of these tests is that the create dialog routes
// failures through the real friendlyErrorMessage (P2.5 W5), so mocking it
// would prove nothing.
const useCreateSpaceMock = vi.fn();
const useSpacesMock = vi.fn(
  (): { data: unknown[]; isLoading: boolean; error: unknown } => ({
    data: [],
    isLoading: false,
    error: null,
  }),
);

vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useSpaces: () => useSpacesMock(),
    useCreateSpace: (orgId: string) => useCreateSpaceMock(orgId),
  };
});

vi.mock('../../../lib/auth', () => ({
  useAuth: vi.fn(() => ({
    user: { id: 'u1', email: 'test@example.com', orgId: 'org-1', role: 'member' },
    isAuthenticated: true,
    login: vi.fn(),
    logout: vi.fn(),
  })),
  getCurrentOrgId: vi.fn(() => 'org-1'),
}));

function mutationState(error: unknown) {
  return {
    error,
    isPending: false,
    mutateAsync: vi.fn(),
  };
}

// /?create=space opens the create dialog on mount — the same entry the top
// bar's Create button uses.
function renderWithDialogOpen() {
  return render(
    <MemoryRouter initialEntries={['/?create=space']}>
      <HomeOverviewPage />
    </MemoryRouter>,
  );
}

function space(id: string, type: 'beacon' | 'codex' | 'vector') {
  return {
    id,
    name: `Space ${id}`,
    slug: `space-${id}`,
    type,
    description: '',
    readable: true,
  };
}

function renderHome() {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <HomeOverviewPage />
    </MemoryRouter>,
  );
}

describe('HomeOverviewPage stat cards', () => {
  beforeEach(() => {
    useCreateSpaceMock.mockReset();
    useCreateSpaceMock.mockReturnValue(mutationState(null));
    useSpacesMock.mockReset();
  });

  // Regression: the stats row rendered Spaces / Beacon / Vector and simply
  // omitted Codex — a whole module absent from the overview.
  it('shows a Codex count card alongside the other modules — regression: missing Codex count', () => {
    useSpacesMock.mockReturnValue({
      data: [space('a', 'beacon'), space('b', 'codex'), space('c', 'codex'), space('d', 'vector')],
      isLoading: false,
      error: null,
    });
    renderHome();

    const codexCard = screen.getByTestId('stat-codex');
    expect(codexCard).toHaveTextContent('2');
    expect(codexCard).toHaveTextContent(/codex spaces/i);
  });

  // Regression: count captions were hard-coded plural — "1 Vector spaces".
  it('pluralises count captions — regression: "1 Vector spaces"', () => {
    useSpacesMock.mockReturnValue({
      data: [space('a', 'beacon'), space('b', 'beacon'), space('c', 'codex'), space('d', 'vector')],
      isLoading: false,
      error: null,
    });
    renderHome();

    expect(screen.getByTestId('stat-vector')).toHaveTextContent('Vector space');
    expect(screen.getByTestId('stat-vector')).not.toHaveTextContent('Vector spaces');
    expect(screen.getByTestId('stat-codex')).toHaveTextContent('Codex space');
    expect(screen.getByTestId('stat-codex')).not.toHaveTextContent('Codex spaces');
    expect(screen.getByTestId('stat-beacon')).toHaveTextContent('Beacon spaces');
    expect(screen.getByTestId('stat-all')).toHaveTextContent('Spaces');
  });
});

describe('HomeOverviewPage create-space error surfacing', () => {
  beforeEach(() => {
    useCreateSpaceMock.mockReset();
    useSpacesMock.mockReset();
    useSpacesMock.mockReturnValue({ data: [], isLoading: false, error: null });
  });

  it('passes a CONFLICT message through verbatim — a slug taken in this module', () => {
    useCreateSpaceMock.mockReturnValue(
      mutationState(
        new APIError(409, {
          error: {
            code: 'CONFLICT',
            message: 'a Vector space with this slug already exists in the organization',
            request_id: 'req-1',
          },
        }),
      ),
    );
    renderWithDialogOpen();

    expect(
      screen.getByText('a Vector space with this slug already exists in the organization'),
    ).toBeInTheDocument();
  });

  it('collapses non-human codes to the fallback — raw backend strings never render', () => {
    useCreateSpaceMock.mockReturnValue(
      mutationState(
        new APIError(400, {
          error: {
            code: 'BAD_REQUEST',
            message: 'invalid request body',
            request_id: 'req-2',
          },
        }),
      ),
    );
    renderWithDialogOpen();

    expect(screen.getByText('The space could not be created.')).toBeInTheDocument();
    expect(screen.queryByText('invalid request body')).toBeNull();
  });
});
