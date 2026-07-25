import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

import {
  CODEX_INLINE_CONTENT_NODES,
  CODEX_INLINE_NODES,
  CODEX_MARKS,
  CODEX_NODES,
  MARK_UNKNOWN_MARK,
  NODE_UNKNOWN_CONTENT,
  NODE_UNKNOWN_INLINE,
  PRESERVED_ATTRS,
  preservedIdsIn,
  type CodexDoc,
} from './schema';

/**
 * The cross-language schema invariant (ADR-0012).
 *
 * The Go layer decides what to preserve by consulting
 * internal/core/wiki/doc/schema.json. The editor decides what to register by
 * consulting schema.ts. If those two lists drift, one of two things happens, and
 * only one of them is survivable:
 *
 * - the editor registers a type Go does not know   -> it gets preserved
 *   unnecessarily. An inert block where a real one was possible. Safe.
 * - Go stops preserving a type the editor does NOT register -> ProseMirror drops
 *   it on load, without a word, and the next save writes the document without it.
 *   That is the exact failure ADR-0012 exists to prevent.
 *
 * Nothing at build time can catch this: Vite cannot bundle a file from outside
 * `web/`, so the editor cannot import the manifest. A test can read it, though —
 * so this is the check, and it fails in both directions on purpose.
 */

const manifestPath = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../internal/core/wiki/doc/schema.json',
);

interface Manifest {
  nodes: Record<string, string>;
  marks: Record<string, string>;
  inlineNodes: string[];
  inlineContentNodes: string[];
}

function readManifest(): Manifest {
  return JSON.parse(readFileSync(manifestPath, 'utf8')) as Manifest;
}

describe('the Codex document schema mirrors the Go manifest', () => {
  it('finds the manifest where the Go package embeds it', () => {
    // A rename or a move on either side must fail loudly here rather than
    // silently skipping the comparison below.
    expect(() => readManifest()).not.toThrow();
  });

  it('registers exactly the node types the server preserves against', () => {
    const manifest = readManifest();
    expect([...CODEX_NODES].sort()).toEqual(Object.keys(manifest.nodes).sort());
  });

  it('registers exactly the mark types the server preserves against', () => {
    const manifest = readManifest();
    expect([...CODEX_MARKS].sort()).toEqual(Object.keys(manifest.marks).sort());
  });

  it('agrees with the server about which nodes are inline', () => {
    // Capture picks between the block and inline placeholder from the position,
    // so a disagreement here puts a block node inside a paragraph.
    const manifest = readManifest();
    expect([...CODEX_INLINE_NODES].sort()).toEqual([...manifest.inlineNodes].sort());
    expect([...CODEX_INLINE_CONTENT_NODES].sort()).toEqual(
      [...manifest.inlineContentNodes].sort(),
    );
  });

  it('carries all three preservation types, or preserved content nests forever', () => {
    // If a placeholder type were missing from the schema, the next shield would
    // treat the placeholder itself as unknown and wrap it again.
    for (const name of [NODE_UNKNOWN_CONTENT, NODE_UNKNOWN_INLINE] as const) {
      expect(CODEX_NODES).toContain(name);
    }
    expect(CODEX_MARKS).toContain(MARK_UNKNOWN_MARK);
  });

  it('declares no inline node it does not also register', () => {
    for (const name of CODEX_INLINE_NODES) {
      expect(CODEX_NODES).toContain(name);
    }
    for (const name of CODEX_INLINE_CONTENT_NODES) {
      expect(CODEX_NODES).toContain(name);
    }
  });
});

describe('preservedIdsIn', () => {
  it('collects placeholder ids in document order', () => {
    const doc: CodexDoc = {
      type: 'doc',
      content: [
        { type: 'paragraph', content: [{ type: 'text', text: 'before' }] },
        { type: NODE_UNKNOWN_CONTENT, attrs: { [PRESERVED_ATTRS.id]: 'u1' } },
        {
          type: 'paragraph',
          content: [
            {
              type: 'text',
              text: 'coloured',
              marks: [{ type: MARK_UNKNOWN_MARK, attrs: { [PRESERVED_ATTRS.id]: 'u2' } }],
            },
            { type: NODE_UNKNOWN_INLINE, attrs: { [PRESERVED_ATTRS.id]: 'u3' } },
          ],
        },
      ],
    };
    expect(preservedIdsIn(doc)).toEqual(['u1', 'u2', 'u3']);
  });

  it('ignores nodes that are not placeholders, and placeholders with no id', () => {
    const doc: CodexDoc = {
      type: 'doc',
      content: [
        { type: 'paragraph', content: [{ type: 'text', text: 'x' }] },
        { type: NODE_UNKNOWN_CONTENT, attrs: { [PRESERVED_ATTRS.name]: 'no id here' } },
        { type: NODE_UNKNOWN_CONTENT, attrs: { [PRESERVED_ATTRS.id]: '' } },
      ],
    };
    expect(preservedIdsIn(doc)).toEqual([]);
  });

  it('survives a null or malformed document rather than throwing', () => {
    // It runs on a server payload, and an editor that crashes on a surprising
    // document is worse than one that reports nothing preserved.
    expect(preservedIdsIn(null)).toEqual([]);
    expect(preservedIdsIn(undefined)).toEqual([]);
  });
});
