import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import { BREAKDOWN_FIELDS, GADGET_LIMITS, GADGET_SPANS, registeredKeys } from './registry';
// The definitions register on import. Without this the registry is empty and
// every assertion below would pass vacuously — which is why the count is
// asserted first.
import '../../components/dashboards/gadgets';

/**
 * The gadget vocabulary, from two directions.
 *
 * internal/core/dashboards/registry.go owns what may be WRITTEN — the closed
 * key set, the four configuration keys and the bounds. This file owns what is
 * DRAWN. A key the server accepts and the client cannot draw renders an
 * "unknown gadget" placeholder for something that is not unknown; a key the
 * client offers and the server refuses is a picker entry whose Add button
 * always 422s. Neither shows up in a type error, so it shows up here.
 *
 * Vite cannot bundle a Go file, but a test running in Node can read one — the
 * same drift check the filter vocabulary and the Codex schema carry. It fails
 * in BOTH directions.
 */

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, '../../../..');

function goSource(relative: string): string {
  return readFileSync(resolve(repoRoot, relative), 'utf8');
}

/** Every `Gadget<Name> GadgetKey = "value"` in the Go registry. */
function goGadgetKeys(): string[] {
  const src = goSource('internal/core/dashboards/registry.go');
  return [...src.matchAll(/Gadget[A-Za-z]+\s+GadgetKey\s*=\s*"([a-z_]+)"/g)]
    .map((m) => m[1])
    .sort();
}

/** Every `Cfg<Name> ConfigKey = "value"` in the Go registry. */
function goConfigKeys(): string[] {
  const src = goSource('internal/core/dashboards/registry.go');
  return [...src.matchAll(/Cfg[A-Za-z]+\s+ConfigKey\s*=\s*"([a-z_]+)"/g)]
    .map((m) => m[1])
    .sort();
}

/** Every `Group<Name> GroupField = "value"` in the Go aggregate vocabulary. */
function goGroupFields(): string[] {
  const src = goSource('internal/core/views/aggregate.go');
  return [...src.matchAll(/Group[A-Za-z]+\s+GroupField\s*=\s*"([a-z_]+)"/g)]
    .map((m) => m[1])
    .filter((v) => v !== '')
    .sort();
}

function goConst(file: string, name: string): number {
  const src = goSource(file);
  const m = new RegExp(`${name}\\s*=\\s*(\\d+)`).exec(src);
  if (!m) throw new Error(`${name} not found in ${file}`);
  return Number(m[1]);
}

describe('gadget registry ↔ internal/core/dashboards/registry.go', () => {
  it('registers every gadget the server defines, and no others', () => {
    const go = goGadgetKeys();
    expect(go.length).toBeGreaterThan(0);
    // The premise: without this, an empty client registry would "match" a
    // failed regex and the whole file would assert nothing.
    expect(registeredKeys().length).toBeGreaterThan(0);
    expect(registeredKeys()).toEqual(go);
  });

  it('mirrors the configuration vocabulary', () => {
    const go = goConfigKeys();
    expect(go).toEqual(['body', 'group_by', 'limit', 'title']);
  });

  it('mirrors the breakdown fields the aggregate endpoint accepts', () => {
    expect([...BREAKDOWN_FIELDS].sort()).toEqual(goGroupFields());
  });

  it('mirrors the bounds, so a control never offers a value the server refuses', () => {
    expect(GADGET_LIMITS.minLimit).toBe(goConst('internal/core/dashboards/registry.go', 'MinGadgetLimit'));
    expect(GADGET_LIMITS.maxLimit).toBe(goConst('internal/core/dashboards/registry.go', 'MaxGadgetLimit'));
    expect(GADGET_LIMITS.defaultLimit).toBe(
      goConst('internal/core/dashboards/registry.go', 'DefaultGadgetLimit'),
    );
    expect(GADGET_LIMITS.maxTitle).toBe(goConst('internal/core/dashboards/registry.go', 'MaxTitleLen'));
    expect(GADGET_LIMITS.maxNote).toBe(goConst('internal/core/dashboards/registry.go', 'MaxNoteLen'));
    expect(GADGET_LIMITS.maxGadgets).toBe(goConst('internal/core/dashboards/registry.go', 'MaxGadgets'));
  });

  it('offers only the spans migration 042 admits', () => {
    const migration = goSource('migrations/042_dashboards.sql');
    const m = /CHECK \(col_span IN \(([^)]*)\)\)/.exec(migration);
    expect(m).not.toBeNull();
    const allowed = (m?.[1] ?? '')
      .split(',')
      .map((v) => Number(v.trim()))
      .sort();
    expect([...GADGET_SPANS].sort()).toEqual(allowed);
  });
});

describe('gadget registry shape', () => {
  it('registers a definition once', async () => {
    const { registerGadget } = await import('./registry');
    expect(() =>
      registerGadget({
        key: 'note',
        name: 'Duplicate',
        description: '',
        icon: (() => null) as never,
        defaultSpan: 1,
        modules: ['home'],
        requiresSavedView: false,
        configKeys: [],
        render: 'note',
        Body: () => null,
      }),
    ).toThrow(/registered twice/);
  });

  it('does not resolve a key it was never given', async () => {
    const { getGadget } = await import('./registry');
    expect(getGadget('sprint_burndown')).toBeUndefined();
  });
});
