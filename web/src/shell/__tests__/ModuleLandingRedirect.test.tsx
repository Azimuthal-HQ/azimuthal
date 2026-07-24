import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ModuleLandingRedirect } from '../ModuleLandingRedirect';

// S10(b): switching modules lands in the current team's space in the target
// module when one exists. Here two Codex spaces are readable; 'cx-other' is
// recents[0], but the active team context is team T which owns 'cx-T'. The
// redirect must prefer 'cx-T'.

const SPACES = [
  { id: 'cx-T', type: 'codex', owner_team_id: 'team-T', readable: true },
  { id: 'cx-other', type: 'codex', owner_team_id: 'team-Other', readable: true },
];

let activeTeamId: string | null = 'team-T';

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return { ...actual, Navigate: ({ to }: { to: string }) => <div data-testid="navigate-to">{to}</div> };
});
vi.mock('../../lib/api', () => ({ useSpaces: () => ({ data: SPACES, isLoading: false }) }));
vi.mock('../../lib/auth', () => ({ useAuth: () => ({ user: { orgId: 'org-1' } }) }));
vi.mock('../hooks/useRecentSpaces', () => ({ useRecentSpaces: () => ({ recents: ['cx-other'] }) }));
vi.mock('../hooks/useActiveTeamContext', () => ({ useActiveTeamContext: () => activeTeamId }));

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path=":module" element={<ModuleLandingRedirect />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('ModuleLandingRedirect module-switch context', () => {
  it("lands in the active team's space in the target module, over recents", () => {
    activeTeamId = 'team-T';
    renderAt('/codex');
    expect(screen.getByTestId('navigate-to').textContent).toBe('/codex/cx-T');
  });

  it('falls back to recents when there is no team context', () => {
    activeTeamId = null;
    renderAt('/codex');
    expect(screen.getByTestId('navigate-to').textContent).toBe('/codex/cx-other');
  });
});
