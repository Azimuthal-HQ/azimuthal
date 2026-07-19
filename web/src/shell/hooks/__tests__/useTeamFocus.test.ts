import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { installLocalStorageStub } from '../../../test/localStorageStub';
import { useTeamFocus } from '../useTeamFocus';

vi.mock('../../../lib/auth', () => ({
  getCurrentOrgId: vi.fn(() => 'org-1'),
}));

// The environment's built-in Storage shim lacks setItem; persistence
// assertions need a real one.
installLocalStorageStub();

const STORAGE_KEY = 'azimuthal_team_focus_org-1';

/** ADR-0006 point 7: focus is real in P2 — per-org, persisted, one store. */
describe('useTeamFocus', () => {
  it('round-trips set and clear, persisting to the per-org localStorage key', () => {
    const { result } = renderHook(() => useTeamFocus());
    expect(result.current.focus).toBeNull();

    act(() => result.current.setFocus('t1', 'Platform'));
    expect(result.current.focus).toEqual({ teamId: 't1', teamName: 'Platform' });
    expect(JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? 'null')).toEqual({
      teamId: 't1',
      teamName: 'Platform',
    });

    act(() => result.current.clearFocus());
    expect(result.current.focus).toBeNull();
    expect(window.localStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it('restores a persisted focus on mount', () => {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify({ teamId: 't2', teamName: 'Design' }));

    const { result } = renderHook(() => useTeamFocus());
    expect(result.current.focus).toEqual({ teamId: 't2', teamName: 'Design' });
  });

  it('treats a corrupt persisted entry as no focus instead of crashing', () => {
    window.localStorage.setItem(STORAGE_KEY, 'not-json{');

    const { result } = renderHook(() => useTeamFocus());
    expect(result.current.focus).toBeNull();
  });

  it('keeps every consumer on the same value (one shared store)', () => {
    const first = renderHook(() => useTeamFocus());
    const second = renderHook(() => useTeamFocus());

    act(() => first.result.current.setFocus('t3', 'Growth'));
    expect(second.result.current.focus).toEqual({ teamId: 't3', teamName: 'Growth' });
  });
});
