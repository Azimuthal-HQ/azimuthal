import { getSchema } from '@tiptap/core';
import { describe, expect, it } from 'vitest';

import { CODEX_MARKS, CODEX_NODES, PRESERVED_ATTRS } from '../../../lib/codex/schema';
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
    const projected: [string, string][] = [
      ['panel', 'kind'],
      ['expand', 'title'],
      ['statusLozenge', 'text'],
      ['pageInclude', 'page_id'],
      ['codeBlock', 'language'],
      ['heading', 'level'],
      ['image', 'attachment_id'],
    ];
    for (const [node, attr] of projected) {
      expect(Object.keys(schema.nodes[node].spec.attrs ?? {}), `${node}.${attr}`).toContain(attr);
    }
    // The link mark falls back to page_id when it has no href, so an internal
    // link still projects as `page:<id>` rather than an empty target.
    expect(Object.keys(schema.marks.link.spec.attrs ?? {})).toContain('page_id');
  });
});
