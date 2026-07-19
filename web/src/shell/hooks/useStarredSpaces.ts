import { useCallback } from 'react';
import { useLocalStorageState } from './useLocalStorageState';

const STORAGE_KEY = 'azimuthal-starred-spaces';

/** useStarredSpaces tracks locally starred space ids for picker pinning. */
export function useStarredSpaces(): {
  starred: string[];
  isStarred: (spaceId: string) => boolean;
  toggleStar: (spaceId: string) => void;
} {
  const [starred, setStarred] = useLocalStorageState<string[]>(STORAGE_KEY, []);

  const isStarred = useCallback((spaceId: string) => starred.includes(spaceId), [starred]);

  const toggleStar = useCallback(
    (spaceId: string) => {
      setStarred((prev) =>
        prev.includes(spaceId) ? prev.filter((id) => id !== spaceId) : [...prev, spaceId],
      );
    },
    [setStarred],
  );

  return { starred, isStarred, toggleStar };
}
