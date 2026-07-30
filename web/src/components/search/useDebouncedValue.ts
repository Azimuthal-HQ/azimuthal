import { useEffect, useState } from 'react';

/**
 * Debounced value, scoped to search rather than added as a shared surface.
 *
 * Every existing debounce in this codebase is a local ref in the component that
 * needs it; this is the same thing given a name, because two search surfaces
 * need it and a third copy would be the one that drifts.
 */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}
