import { useCallback, useState } from 'react';

/**
 * useLocalStorageState is a useState variant persisted under a localStorage
 * key. Parse or storage failures fall back to the initial value so a corrupt
 * entry can never break the shell.
 */
export function useLocalStorageState<T>(key: string, initial: T): [T, (next: T | ((prev: T) => T)) => void] {
  const [value, setValue] = useState<T>(() => {
    try {
      const raw = window.localStorage.getItem(key);
      return raw === null ? initial : (JSON.parse(raw) as T);
    } catch {
      return initial;
    }
  });

  const set = useCallback(
    (next: T | ((prev: T) => T)) => {
      setValue((prev) => {
        const resolved = typeof next === 'function' ? (next as (p: T) => T)(prev) : next;
        try {
          window.localStorage.setItem(key, JSON.stringify(resolved));
        } catch {
          // Persisting is best-effort; in-memory state still updates.
        }
        return resolved;
      });
    },
    [key],
  );

  return [value, set];
}
