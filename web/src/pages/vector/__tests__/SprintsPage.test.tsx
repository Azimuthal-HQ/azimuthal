import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { SprintsPage } from '../SprintsPage';
import type { Sprint } from '../../../lib/api';

// W1 sprint-completion disposition surface. The complete action must open a
// dialog offering "return to backlog" vs "move to another sprint", and pass the
// chosen disposition to the mutation: null next-sprint for backlog, the chosen
// sprint id for carry-over. Done items are handled server-side and are not the
// subject of this test.

const activeSprint: Sprint = {
  id: 'sprint-active', space_id: 's1', name: 'Sprint A', goal: '',
  status: 'active', starts_at: null, ends_at: null,
  created_at: '', updated_at: '',
};
const plannedSprint: Sprint = {
  id: 'sprint-planned', space_id: 's1', name: 'Sprint B', goal: '',
  status: 'planned', starts_at: null, ends_at: null,
  created_at: '', updated_at: '',
};

const completeMutate = vi.fn(async () => activeSprint);

vi.mock('../../../lib/api', () => ({
  useSprints: () => ({ data: [activeSprint, plannedSprint], isLoading: false }),
  useActiveSprint: () => ({ data: activeSprint }),
  useCreateSprint: () => ({ mutateAsync: vi.fn(), isPending: false, error: null }),
  useStartSprint: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useCompleteSprint: () => ({ mutateAsync: completeMutate, isPending: false }),
  friendlyErrorMessage: (_e: unknown, fallback: string) => fallback,
}));

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/vector/s1/sprints']}>
      <Routes>
        <Route path="/vector/:spaceId/sprints" element={<SprintsPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => {
  completeMutate.mockClear();
});

describe('SprintsPage completion disposition', () => {
  it('completes to the backlog by default (null next sprint)', async () => {
    renderPage();
    // The active sprint's Complete button opens the disposition dialog.
    fireEvent.click(screen.getByRole('button', { name: /^Complete$/ }));
    expect(await screen.findByText('Complete Sprint A')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Complete Sprint$/ }));
    await waitFor(() => expect(completeMutate).toHaveBeenCalledTimes(1));
    expect(completeMutate).toHaveBeenCalledWith({ sprintId: 'sprint-active', nextSprintId: null });
  });

  it('carries incomplete items over to a chosen sprint', async () => {
    renderPage();
    fireEvent.click(screen.getByRole('button', { name: /^Complete$/ }));
    await screen.findByText('Complete Sprint A');

    // Choose "move to another sprint" — the carry-over select appears,
    // defaulted to the only candidate (the planned sprint).
    fireEvent.click(screen.getByRole('radio', { name: /Move to another sprint/ }));
    const select = screen.getByLabelText('Carry-over sprint') as HTMLSelectElement;
    expect(select.value).toBe('sprint-planned');

    fireEvent.click(screen.getByRole('button', { name: /Complete Sprint$/ }));
    await waitFor(() => expect(completeMutate).toHaveBeenCalledTimes(1));
    expect(completeMutate).toHaveBeenCalledWith({ sprintId: 'sprint-active', nextSprintId: 'sprint-planned' });
  });
});
