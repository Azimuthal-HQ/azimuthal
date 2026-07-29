import { getSchema } from '@tiptap/core';
import { describe, expect, it } from 'vitest';

import {
  CODEX_MARKS,
  CODEX_NODES,
  PRESERVED_ATTRS,
  PROJECTED_ATTRS,
} from '../../../lib/codex/schema';
import { codexExtensions, registeredTypes } from './index';

/**
 * The third link in ADR-0012's schema chain.
 *
 * `schema.test.ts` proves the TypeScript mirror equals the Go manifest. That
 * leaves the gap that matters most: the mirror is a *list*, and nothing in it
 * makes the editor actually register what it names. An editor that omits a
 * type the manifest promises it handles is the silent-data-loss case — the
 * server stops capturing that type because the schema says the editor deals
 * with it, ProseMirror drops it on load without a word, and the next save
 * writes the document without it.
 *
 * So this asks the real ProseMirror schema what got registered and compares
 * that. It fails in both directions on purpose: an unregistered promise loses
 * data, and a registered surprise (StarterKit's `underline`, the concrete
 * case) lets an author apply formatting the server never agreed to preserve.
 */
describe('the editor registers exactly the schema vocabulary', () => {
  const registered = registeredTypes(codexExtensions());
  const schema = getSchema(codexExtensions());

  it('registers every node type the manifest names, and no others', () => {
    expect(registered.nodes).toEqual([...CODEX_NODES].sort());
  });

  it('registers every mark type the manifest names, and no others', () => {
    expect(registered.marks).toEqual([...CODEX_MARKS].sort());
  });

  it('does not register underline, which StarterKit ships and the schema omits', () => {
    // Named rather than left to the set comparison above, because this one is
    // not hypothetical: it is on by default, and turning it back on is a
    // one-word change somebody will be tempted to make.
    expect(registered.marks).not.toContain('underline');
  });

  it('makes the block and inline carriers atoms in the right positions', () => {
    // A placeholder whose interior could be typed into would produce a document
    // whose displayed body no longer matches the bytes the server writes back.
    // And a block carrier in an inline position (or the reverse) is dropped by
    // ProseMirror, which is the loss this whole mechanism exists to prevent.
    expect(schema.nodes.unknownContent.isAtom).toBe(true);
    expect(schema.nodes.unknownContent.isBlock).toBe(true);
    expect(schema.nodes.unknownInline.isAtom).toBe(true);
    expect(schema.nodes.unknownInline.isInline).toBe(true);
  });

  it('declares every preservation attribute on all three carriers', () => {
    // az_id is the one that cannot be lost: without it Restore cannot tell
    // which captured original a placeholder stands for, and publish refuses
    // the whole write.
    const carriers = [
      schema.nodes.unknownContent,
      schema.nodes.unknownInline,
      schema.marks.unknownMark,
    ];
    for (const carrier of carriers) {
      const declared = Object.keys(carrier.spec.attrs ?? {});
      for (const attr of Object.values(PRESERVED_ATTRS)) {
        expect(declared).toContain(attr);
      }
    }
  });

  it('keeps the attribute names the markdown projection reads', () => {
    // internal/core/wiki/doc/text.go reads these by name to build the markdown
    // that feeds the generated search_vector. Renaming one here fails nothing
    // at runtime — the page publishes, the document stores correctly, and the
    // content quietly stops being findable.
    //
    // The list is no longer written out here. It is PROJECTED_ATTRS, which
    // schema.test.ts holds equal to the Go manifest — so this test asks the
    // real ProseMirror schema whether it declares what the manifest promises,
    // rather than whether it declares a second list somebody kept in step by
    // hand. A hand-kept copy is exactly the drift this chain exists to catch.
    for (const [node, attrs] of Object.entries(PROJECTED_ATTRS.nodes)) {
      const declared = Object.keys(schema.nodes[node]?.spec.attrs ?? {});
      for (const attr of attrs) {
        expect(declared, `${node}.${attr}`).toContain(attr);
      }
    }
    for (const [mark, attrs] of Object.entries(PROJECTED_ATTRS.marks)) {
      const declared = Object.keys(schema.marks[mark]?.spec.attrs ?? {});
      for (const attr of attrs) {
        expect(declared, `${mark}.${attr}`).toContain(attr);
      }
    }
  });

  it('makes the inline tag an inline atom', () => {
    // A tag whose interior could be typed into would let an author edit the
    // label out from under the token, producing a document whose visible text
    // and whose stored `label` attribute disagree — and the label is what the
    // server aggregates into the page's tags at publish.
    expect(schema.nodes.inlineTag.isAtom).toBe(true);
    expect(schema.nodes.inlineTag.isInline).toBe(true);
  });
});
