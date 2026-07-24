import { useSyncExternalStore } from 'react';
import { getCurrentOrgId } from '../../lib/auth';

/**
 * useActiveTeamContext records the owning team of the space the user is
 * currently working in, per-org, so switching modules can prefer that team's
 * space in the target module. It is DELIBERATELY separate from useTeamFocus:
 * that is the explicit "narrow by choice" filter (ADR-0006 point 7), and
 * auto-setting it on every visit would corrupt its contract and pop the
 * FocusChip. This store never surfaces UI — it only steers default landing.
 *
 * The snapshot is a plain teamId string (or null); strings compare by value,
 * so useSyncExternalStore stays stable without extra caching.
 */
const listeners = new Set<() => void>();

function storageKey(orgId: string): string {
  return `azimuthal_active_team_${orgId}`;
}

// currentOrgId never throws: a broken token store degrades to "no context".
function currentOrgId(): string {
  try {
    return getCurrentOrgId();
  } catch {
    return '';
  }
}

function getSnapshot(): string | null {
  const orgId = currentOrgId();
  if (!orgId) return null;
  try {
    return window.localStorage.getItem(storageKey(orgId));
  } catch {
    return null;
  }
}

function emit(): void {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  window.addEventListener('storage', listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener('storage', listener);
  };
}

/** Records the team whose space the user is currently in (per-org). */
export function setActiveTeamContext(teamId: string): void {
  const orgId = currentOrgId();
  if (!orgId || !teamId) return;
  try {
    if (window.localStorage.getItem(storageKey(orgId)) === teamId) return;
    window.localStorage.setItem(storageKey(orgId), teamId);
  } catch {
    // Best-effort persistence; the snapshot re-reads storage.
  }
  emit();
}

/** The active team context for the current org, or null. */
export function useActiveTeamContext(): string | null {
  return useSyncExternalStore(subscribe, getSnapshot);
}
