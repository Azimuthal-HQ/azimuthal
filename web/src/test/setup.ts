import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';

afterEach(() => {
  cleanup();
  // Some jsdom Storage shims lack clear(); remove keys individually.
  const storage = window.localStorage;
  if (typeof storage.clear === 'function') {
    storage.clear();
  } else {
    for (const key of Object.keys(storage)) storage.removeItem(key);
  }
});
