/**
 * installLocalStorageStub replaces window.localStorage with a functional
 * in-memory Storage for tests that WRITE to it. Some Node/jsdom pairings
 * ship a partial Storage where setItem is missing entirely (setup.ts
 * already works around the same shim lacking clear()), so any test
 * asserting persistence must install this first.
 */
export function installLocalStorageStub(): void {
  const store = new Map<string, string>();
  const stub: Storage = {
    get length() {
      return store.size;
    },
    clear: () => {
      store.clear();
    },
    getItem: (key: string) => (store.has(key) ? store.get(key)! : null),
    key: (index: number) => [...store.keys()][index] ?? null,
    removeItem: (key: string) => {
      store.delete(key);
    },
    setItem: (key: string, value: string) => {
      store.set(key, String(value));
    },
  };
  Object.defineProperty(window, 'localStorage', {
    value: stub,
    configurable: true,
    writable: true,
  });
}
