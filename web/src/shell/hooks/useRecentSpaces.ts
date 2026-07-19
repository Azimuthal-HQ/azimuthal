import { useCallback } from 'react';
import type { ModuleKey } from '../modules';
import { useLocalStorageState } from './useLocalStorageState';

const STORAGE_KEY = 'azimuthal-recent-spaces';
const MAX_RECENTS = 5;

type RecentsByModule = Partial<Record<ModuleKey, string[]>>;

/**
 * useRecentSpaces tracks the most recently visited space ids per module,
 * newest first, persisted locally. Drives the space picker's pinned group
 * and the /:module landing redirect.
 */
export function useRecentSpaces(module: ModuleKey): {
  recents: string[];
  recordVisit: (spaceId: string) => void;
} {
  const [byModule, setByModule] = useLocalStorageState<RecentsByModule>(STORAGE_KEY, {});

  const recordVisit = useCallback(
    (spaceId: string) => {
      setByModule((prev) => {
        const existing = prev[module] ?? [];
        if (existing[0] === spaceId) return prev;
        const next = [spaceId, ...existing.filter((id) => id !== spaceId)].slice(0, MAX_RECENTS);
        return { ...prev, [module]: next };
      });
    },
    [module, setByModule],
  );

  return { recents: byModule[module] ?? [], recordVisit };
}
