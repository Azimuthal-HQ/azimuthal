import { describe, expect, it } from 'vitest';

// Spec §7, API access: "All network calls go through web/src/lib/api.ts.
// No fetch in components, no second client, no exceptions."
//
// WorkflowAdminPage violated this with a private fetch client reading a
// never-written localStorage token key — every request 401'd and the page
// rendered its error state for every user, silently, since it shipped.
// This test makes the rule mechanical: the global fetch may appear nowhere
// under src/ except lib/api.ts.

// Raw source of every module under src/, resolved at transform time by Vite —
// no Node fs access, so it type-checks under the app tsconfig (vite/client).
const sources = import.meta.glob('../**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

// The one sanctioned call site. Glob keys are relative to this file's
// directory, so lib/api.ts — our sibling — appears as './api.ts'.
const ALLOWED = new Set(['./api.ts']);

// A bare (or window./globalThis.-qualified) global fetch call. The lookbehind
// rejects member access and identifier tails, so apiFetch(, refetch( and
// queryClient.fetchQuery( do not match.
const DIRECT_FETCH = /(?<![.\w$])(?:window\.|globalThis\.)?fetch\s*\(/;

function isProductionSource(path: string): boolean {
  if (ALLOWED.has(path)) return false;
  if (path.endsWith('.d.ts')) return false;
  if (/\.(test|spec)\.tsx?$/.test(path)) return false;
  if (path.startsWith('../test/')) return false; // vitest setup
  return true;
}

describe('spec §7 — single api client', () => {
  it('no direct fetch() outside lib/api.ts', () => {
    const violations = Object.entries(sources)
      .filter(([path]) => isProductionSource(path))
      .flatMap(([path, content]) =>
        content
          .split('\n')
          .map((line, i) => ({ line, i }))
          .filter(({ line }) => DIRECT_FETCH.test(line))
          .map(({ line, i }) => `${path}:${i + 1}  ${line.trim()}`),
      );

    expect(violations, 'network calls must go through lib/api.ts').toEqual([]);
  });
});
