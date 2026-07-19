import { useCallback, useSyncExternalStore } from 'react';
import { getCurrentOrgId } from '../../lib/auth';

/**
 * useTeamFocus is the team-focus filter (ADR-0006 point 7), real as of P2:
 * a module-level store persisted per-org under
 * `azimuthal_team_focus_<orgId>`, exposed through useSyncExternalStore so
 * every consumer (FocusChip, SpacePicker, the directory) sees one value.
 * Focus narrows what pickers show; consumers must always surface how much
 * it hides (union by default, narrow by choice).
 */
export interface TeamFocus {
  teamId: string;
  teamName: string;
}

const listeners = new Set<() => void>();

function storageKey(orgId: string): string {
  return `azimuthal_team_focus_${orgId}`;
}

// currentOrgId never throws: a broken token store must degrade to "no
// focus", not break every consumer of the shell.
function currentOrgId(): string {
  try {
    return getCurrentOrgId();
  } catch {
    return '';
  }
}

function parseFocus(raw: string | null): TeamFocus | null {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as { teamId?: unknown; teamName?: unknown } | null;
    if (parsed && typeof parsed.teamId === 'string' && typeof parsed.teamName === 'string') {
      return { teamId: parsed.teamId, teamName: parsed.teamName };
    }
  } catch {
    // Corrupt entry — treat as no focus; never break the shell.
  }
  return null;
}

// getSnapshot must return a referentially stable value while nothing
// changed, so the parsed focus is cached against the raw string it came
// from (and the org it was read for).
let lastOrgId: string | null = null;
let lastRaw: string | null = null;
let lastFocus: TeamFocus | null = null;

function getSnapshot(): TeamFocus | null {
  const orgId = currentOrgId();
  let raw: string | null = null;
  if (orgId) {
    try {
      raw = window.localStorage.getItem(storageKey(orgId));
    } catch {
      raw = null;
    }
  }
  if (orgId !== lastOrgId || raw !== lastRaw) {
    lastOrgId = orgId;
    lastRaw = raw;
    lastFocus = parseFocus(raw);
  }
  return lastFocus;
}

function emit(): void {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  // Cross-tab: another tab changing the focus fires 'storage' here.
  window.addEventListener('storage', listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener('storage', listener);
  };
}

/** Sets the active team focus for the current org (persisted). */
export function setTeamFocus(teamId: string, teamName: string): void {
  const orgId = currentOrgId();
  if (!orgId) return;
  try {
    window.localStorage.setItem(storageKey(orgId), JSON.stringify({ teamId, teamName }));
  } catch {
    // Persisting is best-effort; the snapshot re-reads storage, so a failed
    // write simply leaves the focus unchanged.
  }
  emit();
}

/** Clears the active team focus for the current org. */
export function clearTeamFocus(): void {
  const orgId = currentOrgId();
  if (!orgId) return;
  try {
    window.localStorage.removeItem(storageKey(orgId));
  } catch {
    // Best-effort — see setTeamFocus.
  }
  emit();
}

export function useTeamFocus(): {
  focus: TeamFocus | null;
  setFocus: (teamId: string, teamName: string) => void;
  clearFocus: () => void;
} {
  const focus = useSyncExternalStore(subscribe, getSnapshot);
  const setFocus = useCallback(
    (teamId: string, teamName: string) => setTeamFocus(teamId, teamName),
    [],
  );
  const clearFocus = useCallback(() => clearTeamFocus(), []);
  return { focus, setFocus, clearFocus };
}
