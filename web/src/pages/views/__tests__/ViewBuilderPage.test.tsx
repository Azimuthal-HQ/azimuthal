import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { ViewBuilderPage } from '../ViewBuilderPage';
import { VIEW_DRAFT_STATE_KEY } from '../../../lib/views/draft';
import type { ViewRequest } from '../../../lib/api';

// P4: /views/new is the far end of "Save as view".
//
// SaveAsViewButton already ships on the ticket list and the backlog and
// navigates here with a QueryDoc in router location state. Until this route
// existed the click landed on the not-found page, so what these tests pin is
// the seam: the draft arrives, it prefills the builder, and it is the document
// that reaches the API — unchanged, and without the page having to re-derive
// it from anything on screen.

const created = vi.hoisted(() => ({ mutateAsync: vi.fn() }));

vi.mock('../../../lib/api', () => ({
  useSavedView: () => ({ data: undefined, error: null }),
  useCreateView: () => ({ mutateAsync: created.mutateAsync, isPending: false, error: null }),
  useUpdateView: () => ({ mutateAsync: vi.fn(), isPending: false, error: null }),
  usePreviewResults: () => ({ mutate: vi.fn(), data: undefined, isPending: false, error: null }),
  useSpaces: () => ({ data: [{ id: 's1', name: 'Support', slug: 'sup', key: 'SD', type: 'beacon' }] }),
  useItemTypes: () => ({ data: [] }),
  useSprints: () => ({ data: [] }),
  useMemberSearch: () => ({ data: [], isLoading: false }),
  useTeams: () => ({ data: [] }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'u1', email: 'me@example.com', orgId: 'org-1', role: 'member' } }),
}));

const draft = {
  name: 'Tickets in Support',
  query: {
    v: 1,
    filter: { modules: ['beacon'], space_ids: ['s1'], statuses: ['open'], text: 'outage' },
    sort: { field: 'updated_at', dir: 'desc' },
  },
};

/** Reports where a successful save navigated to. */
function LandedProbe() {
  const { pathname } = useLocation();
  return <span data-testid="landed">{pathname}</span>;
}

function renderBuilder(state: unknown) {
  return render(
    <MemoryRouter initialEntries={[{ pathname: '/views/new', state }]}>
      <Routes>
        <Route path="/views/new" element={<ViewBuilderPage />} />
        <Route path="/views/:viewId" element={<LandedProbe />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('ViewBuilderPage — the /views/new draft seam', () => {
  it('prefills the name and the filters a list page handed over', () => {
    renderBuilder({ [VIEW_DRAFT_STATE_KEY]: draft });

    expect(screen.getByTestId('view-name')).toHaveValue('Tickets in Support');
    expect(screen.getByTestId('view-text')).toHaveValue('outage');
    expect(screen.getByRole('button', { name: 'Beacon' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Vector' })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByTestId('view-status-open')).toBeInTheDocument();
  });

  it('sends the drafted document unchanged, at private visibility', async () => {
    created.mutateAsync.mockResolvedValueOnce({ id: 'v-new' });
    renderBuilder({ [VIEW_DRAFT_STATE_KEY]: draft });

    fireEvent.click(screen.getByTestId('save-view'));

    await waitFor(() => expect(created.mutateAsync).toHaveBeenCalledTimes(1));
    const req = created.mutateAsync.mock.calls[0][0] as ViewRequest;
    expect(req).toEqual({
      name: 'Tickets in Support',
      description: '',
      query: draft.query,
      // No list page can imply an audience, so the builder starts private and
      // the person chooses. `visibility_team_id` is null unless team is chosen.
      visibility: 'private',
      visibility_team_id: null,
    });

    await screen.findByTestId('landed');
    expect(screen.getByTestId('landed')).toHaveTextContent('/views/v-new');
  });

  it('opens on the broad default when the location state is not a draft', () => {
    renderBuilder({ draft: { totally: 'wrong' } });

    expect(screen.getByTestId('view-name')).toHaveValue('');
    // emptyQueryDoc(): every module, no filters.
    expect(screen.getByRole('button', { name: 'Beacon' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: 'Vector' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('refuses to save without a name, and says why', () => {
    renderBuilder(undefined);

    expect(screen.getByTestId('save-view')).toBeDisabled();
    expect(screen.getByTestId('save-problem')).toHaveTextContent('Give this view a name.');
  });

  it('refuses to save a team-visible view with no team chosen', () => {
    renderBuilder({ [VIEW_DRAFT_STATE_KEY]: draft });

    fireEvent.click(screen.getByRole('radio', { name: 'Team' }));

    expect(screen.getByTestId('save-view')).toBeDisabled();
    expect(screen.getByTestId('save-problem')).toHaveTextContent(
      'Choose the team this view is shared with.',
    );
  });
});
