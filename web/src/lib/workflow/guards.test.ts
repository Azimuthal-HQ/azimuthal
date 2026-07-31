import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import {
  APPROVAL_ENTITY_TYPES,
  APPROVER_SUBJECT_TYPES,
  DECISIONS,
  GUARD_CAPABILITIES,
  GUARD_CAPABILITY_LABEL,
  GUARD_CLASSES,
  GUARD_CLASS_LABEL,
  GUARD_FIELD_KEYS,
  GUARD_FIELD_LABEL,
  GUARD_KINDS,
  GUARD_KIND_LABEL,
  GUARD_KIND_PARAM,
  POST_FIELD_KEYS,
  POST_FIELD_LABEL,
  POST_FUNCTION_KINDS,
  POST_FUNCTION_KIND_LABEL,
} from './vocabulary';

/**
 * The cross-language guard-vocabulary invariant (ADR-0011).
 *
 * `internal/core/workflow/guard.go` opens by naming the four things that depend
 * on this vocabulary and will drift the moment it is duplicated — and it names
 * this file as the thing that stops the drift:
 *
 *     the admin UI's pickers, which may only offer what this file enumerates
 *     (mirrored in TypeScript and held equal by web/src/lib/workflow/guards.test.ts)
 *
 * Until this PR that sentence described intent as fact: neither the mirror nor
 * this test existed, and the pickers it describes had never been built.
 *
 * # Which way the failures point
 *
 * Unlike ADR-0012's editor schema, where one direction is safe and the other
 * loses data, BOTH directions are bad here and neither is silent-but-harmless:
 *
 * - a value in the UI that Go does not know  -> the admin picks it, the POST
 *   is refused by ValidateGuard with a message about a vocabulary the user
 *   cannot see, and the form looks broken.
 * - a value in Go the UI does not offer      -> a rule the product supports is
 *   unreachable through the only surface that configures it, and nothing
 *   anywhere reports that it is missing. This is the worse one, because it
 *   presents as a complete form.
 *
 * So the assertions below are set equality, not containment, in every case.
 *
 * # Why parse Go rather than share a manifest
 *
 * The Codex schema had a JSON manifest to read because the Go side already
 * embedded one. This vocabulary has no manifest — guard.go says outright that
 * it "is the single place the workflow guard vocabulary is defined" — and
 * introducing one to serve a test would create a third copy to keep in step.
 * Reading the declarations is uglier and has no such failure mode.
 */

const goRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../../../internal/core');

function readGo(relativePath: string): string {
  return readFileSync(resolve(goRoot, relativePath), 'utf8');
}

/**
 * Collect the string literals of every `const X SomeType = "value"` declaration.
 *
 * The type token must be preceded by whitespace, which is what keeps `FieldKey`
 * from also matching `PostFieldKey` — a real hazard, because those two
 * vocabularies are deliberately different sizes and silently swapping them is
 * the mistake the guard/post-function asymmetry invites.
 */
function goConstValues(source: string, typeName: string): string[] {
  const pattern = new RegExp(`\\s${typeName}\\s*=\\s*"([^"]+)"`, 'g');
  return [...source.matchAll(pattern)].map((m) => m[1]);
}

/** Collect the identifiers listed in a `var name = []T{ ... }` block. */
function goSliceIdentifiers(source: string, varName: string): string[] {
  const block = new RegExp(`var\\s+${varName}\\s*=\\s*\\[\\][\\w.]+\\{([^}]*)\\}`).exec(source);
  if (!block) return [];
  return block[1]
    .split(',')
    .map((entry) => entry.trim())
    .filter((entry) => entry.length > 0 && !entry.startsWith('//'));
}

const guardGo = readGo('workflow/guard.go');
const postFunctionGo = readGo('workflow/postfunction.go');
const approvalGo = readGo('workflow/approval.go');
const capabilityGo = readGo('access/capability.go');

/** Sorted set equality — order is not part of the contract, membership is. */
function sameSet(a: readonly string[], b: readonly string[]): void {
  expect([...a].sort()).toEqual([...new Set(b)].sort());
}

describe('the workflow tier vocabulary mirrors Go', () => {
  it('finds the Go sources where guard.go says the vocabulary lives', () => {
    // If this fails, every assertion below would pass vacuously against an
    // empty parse. It is the check that keeps the rest from becoming decoration
    // after somebody moves a file.
    expect(guardGo).toContain('type GuardKind string');
    expect(postFunctionGo).toContain('type PostFunctionKind string');
    expect(approvalGo).toContain('type ApproverSubjectType string');
    expect(capabilityGo).toContain('type Capability string');
  });

  it('extracts a non-empty vocabulary from each declaration', () => {
    // The regexes are the load-bearing part of this file. A Go reformat that
    // broke one would otherwise turn its assertion into `[] === []`.
    expect(goConstValues(guardGo, 'GuardClass').length).toBeGreaterThan(0);
    expect(goConstValues(guardGo, 'GuardKind').length).toBeGreaterThan(0);
    expect(goConstValues(guardGo, 'FieldKey').length).toBeGreaterThan(0);
    expect(goConstValues(postFunctionGo, 'PostFunctionKind').length).toBeGreaterThan(0);
    expect(goConstValues(postFunctionGo, 'PostFieldKey').length).toBeGreaterThan(0);
    expect(goConstValues(approvalGo, 'ApproverSubjectType').length).toBeGreaterThan(0);
    expect(goConstValues(approvalGo, 'Decision').length).toBeGreaterThan(0);
    expect(goConstValues(approvalGo, 'ApprovalEntityType').length).toBeGreaterThan(0);
    expect(goSliceIdentifiers(guardGo, 'guardCapabilities').length).toBeGreaterThan(0);
  });

  it('agrees on guard classes', () => {
    sameSet(GUARD_CLASSES, goConstValues(guardGo, 'GuardClass'));
  });

  it('agrees on guard kinds', () => {
    sameSet(GUARD_KINDS, goConstValues(guardGo, 'GuardKind'));
  });

  it('agrees on the fields a guard may require', () => {
    sameSet(GUARD_FIELD_KEYS, goConstValues(guardGo, 'FieldKey'));
  });

  it('agrees on post-function kinds', () => {
    sameSet(POST_FUNCTION_KINDS, goConstValues(postFunctionGo, 'PostFunctionKind'));
  });

  it('agrees on the fields a post-function may write', () => {
    sameSet(POST_FIELD_KEYS, goConstValues(postFunctionGo, 'PostFieldKey'));
  });

  it('keeps the two field vocabularies distinct', () => {
    // Guards READ four fields; post-functions WRITE two. They are separate Go
    // types and separate CHECK constraints, and offering the guard list in a
    // post-function picker is a 400 the admin cannot diagnose. This asserts the
    // asymmetry is real rather than an accident nobody noticed.
    expect(GUARD_FIELD_KEYS.length).toBeGreaterThan(POST_FIELD_KEYS.length);
    for (const key of POST_FIELD_KEYS) {
      expect(GUARD_FIELD_KEYS).toContain(key);
    }
    expect(GUARD_FIELD_KEYS).toContain('description');
    expect(POST_FIELD_KEYS).not.toContain('description');
  });

  it('agrees on approver subject types, and still refuses "role"', () => {
    sameSet(APPROVER_SUBJECT_TYPES, goConstValues(approvalGo, 'ApproverSubjectType'));
    // ADR-0011 names three; two are representable. This is pinned separately
    // because the failure mode is somebody adding the third from the ADR rather
    // than from the code.
    expect(APPROVER_SUBJECT_TYPES).not.toContain('role');
  });

  it('agrees on decisions, and has no third state for "pending"', () => {
    sameSet(DECISIONS, goConstValues(approvalGo, 'Decision'));
    // Pending is the ABSENCE of a decision (decided_at IS NULL), never a value.
    expect(DECISIONS).not.toContain('pending');
    expect(DECISIONS).not.toContain('expired');
  });

  it('agrees on approval entity types', () => {
    sameSet(APPROVAL_ENTITY_TYPES, goConstValues(approvalGo, 'ApprovalEntityType'));
    // "item", not "project_item" — Workflow.applies_to spells the same concept
    // "project_items" and the two must not be swapped.
    expect(APPROVAL_ENTITY_TYPES).toContain('item');
    expect(APPROVAL_ENTITY_TYPES).not.toContain('project_item');
  });

  it('agrees on the capabilities a guard may name, through the same indirection Go uses', () => {
    // guard.go lists access CONSTANTS rather than wire strings, deliberately, so
    // "a capability rename cannot leave a stale copy here". Resolving the
    // identifiers the same way means a rename in capability.go fails this test
    // instead of silently producing a picker offering a value the server
    // refuses — which is the entire point of that indirection.
    const identifiers = goSliceIdentifiers(guardGo, 'guardCapabilities');
    const wireValues = identifiers.map((identifier) => {
      const bare = identifier.replace(/^access\./, '');
      const match = new RegExp(`\\s${bare}\\s+Capability\\s*=\\s*"([^"]+)"`).exec(capabilityGo);
      expect(match, `capability constant ${identifier} not found in access/capability.go`).not.toBeNull();
      return match![1];
    });

    sameSet(GUARD_CAPABILITIES, wireValues);

    // And it is a strict subset: a picker built from the whole capability table
    // would offer values ValidateGuard refuses, and migration 046 does NOT
    // constrain this column, so nothing below the API would catch it.
    const allCapabilities = goConstValues(capabilityGo, 'Capability');
    expect(allCapabilities.length).toBeGreaterThan(GUARD_CAPABILITIES.length);
  });
});

describe('every vocabulary member is renderable', () => {
  // A label map keyed by the wire value makes a missing label a type error, but
  // only for members that exist at compile time. These catch the other half:
  // a value added to Go and mirrored here, with no label written, renders as a
  // blank row in a picker rather than failing anything.
  it('labels every guard kind, class, field and capability', () => {
    for (const kind of GUARD_KINDS) expect(GUARD_KIND_LABEL[kind]).toBeTruthy();
    for (const cls of GUARD_CLASSES) expect(GUARD_CLASS_LABEL[cls]).toBeTruthy();
    for (const field of GUARD_FIELD_KEYS) expect(GUARD_FIELD_LABEL[field]).toBeTruthy();
    for (const cap of GUARD_CAPABILITIES) expect(GUARD_CAPABILITY_LABEL[cap]).toBeTruthy();
  });

  it('labels every post-function kind and writable field', () => {
    for (const kind of POST_FUNCTION_KINDS) expect(POST_FUNCTION_KIND_LABEL[kind]).toBeTruthy();
    for (const field of POST_FIELD_KEYS) expect(POST_FIELD_LABEL[field]).toBeTruthy();
  });

  it('says which parameter every guard kind takes', () => {
    // Mirrors migration 046's shape CHECK. A kind with no entry would render an
    // input-less row that POSTs a shape the server refuses.
    for (const kind of GUARD_KINDS) {
      expect(GUARD_KIND_PARAM[kind]).toBeTruthy();
    }
    expect(GUARD_KIND_PARAM.actor_is_assignee).toBe('none');
    expect(GUARD_KIND_PARAM.actor_in_team).toBe('team');
    expect(GUARD_KIND_PARAM.actor_has_capability).toBe('capability');
    expect(GUARD_KIND_PARAM.field_required).toBe('field');
  });
});
