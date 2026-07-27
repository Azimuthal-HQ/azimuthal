import { getSchema } from '@tiptap/core';
import { Node as PMNode } from '@tiptap/pm/model';
import { describe, expect, it } from 'vitest';

import {
  MARK_UNKNOWN_MARK,
  NODE_UNKNOWN_CONTENT,
  NODE_UNKNOWN_INLINE,
  PRESERVED_ATTRS,
  preservedIdsIn,
} from '../../../lib/codex/schema';
import type { CodexDoc } from '../../../lib/codex/schema';
import { codexExtensions } from './index';

/**
 * ADR-0012's round-trip requirement, at the layer where it is actually at
 * risk.
 *
 * The server's guarantee is that the bytes written back are the bytes that
 * were read, and it holds because the originals never pass through the client
 * — `Restore` resolves them from its own captured map. But that map is keyed
 * by placeholder id, so the guarantee has one client-side precondition: **the
 * placeholders must survive ProseMirror**, carrying their ids.
 *
 * ProseMirror is where they would be lost, and it loses things silently. A
 * carrier registered in the wrong position, an attribute not declared on the
 * node spec, a mark that gets split — each produces a document that looks
 * fine and publishes with content missing, or with an id the server cannot
 * resolve.
 *
 * So this drives the real schema: parse a server payload the way the editor
 * does, serialise it back the way the editor does, and compare. No React and
 * no node views, deliberately — those are presentation, and this is about
 * whether the document survives.
 */

const schema = getSchema(codexExtensions());

/**
 * Parse, validate and re-serialise a document exactly as the editor does.
 *
 * `check()` is not optional here, and leaving it out was a defect this test
 * caught in itself: `Node.fromJSON` builds nodes without validating content
 * models, so a carrier registered in the wrong position — the inline
 * placeholder declared as a block, say — round-tripped perfectly through
 * `fromJSON`/`toJSON` and the test passed. In a real editor that same document
 * has an inline node sitting where ProseMirror will not accept one, and the
 * node is dropped. `check()` is what makes this test model the editor rather
 * than model JSON.
 */
function roundTrip(doc: CodexDoc): CodexDoc {
  const node = PMNode.fromJSON(schema, doc);
  node.check();
  return node.toJSON() as CodexDoc;
}

function placeholder(type: string, id: string, name: string, raw: string, text: string) {
  return {
    type,
    attrs: {
      [PRESERVED_ATTRS.id]: id,
      [PRESERVED_ATTRS.name]: name,
      [PRESERVED_ATTRS.source]: 'document',
      [PRESERVED_ATTRS.raw]: raw,
      [PRESERVED_ATTRS.text]: text,
    },
  };
}

/**
 * A payload shaped like the server's: all three carriers, and an `az_raw`
 * full of the angle brackets a preserved Confluence macro is mostly made of.
 */
const RAW_MACRO =
  '{"type":"ac:structured-macro","attrs":{"ac:name":"gliffy"},"body":"<p>a & b < c > d</p>"}';

const SERVER_DOC: CodexDoc = {
  type: 'doc',
  content: [
    { type: 'paragraph', content: [{ type: 'text', text: 'before' }] },
    placeholder(NODE_UNKNOWN_CONTENT, 'u1', 'ac:structured-macro', RAW_MACRO, 'a & b < c > d'),
    {
      type: 'paragraph',
      content: [
        {
          type: 'text',
          text: 'coloured text',
          marks: [
            {
              type: MARK_UNKNOWN_MARK,
              attrs: {
                [PRESERVED_ATTRS.id]: 'u2',
                [PRESERVED_ATTRS.name]: 'textColor',
                [PRESERVED_ATTRS.source]: 'document',
                [PRESERVED_ATTRS.raw]: '{"type":"textColor","attrs":{"color":"#ff0000"}}',
                [PRESERVED_ATTRS.text]: '',
              },
            },
          ],
        },
        placeholder(NODE_UNKNOWN_INLINE, 'u3', 'ac:emoticon', '{"type":"ac:emoticon"}', ':)'),
      ],
    },
    { type: 'paragraph', content: [{ type: 'text', text: 'after' }] },
  ],
};

describe('preservation carriers survive ProseMirror', () => {
  it('round-trips a document containing all three carriers unchanged', () => {
    expect(roundTrip(SERVER_DOC)).toEqual(SERVER_DOC);
  });

  it('keeps every placeholder id, in order', () => {
    // The ids are the whole mechanism: Restore resolves originals by id, and
    // an id that goes missing is reported as lost content and refuses the
    // publish. This is the assertion that would catch a carrier registered in
    // the wrong position, because ProseMirror drops those without a word.
    expect(preservedIdsIn(roundTrip(SERVER_DOC))).toEqual(['u1', 'u2', 'u3']);
  });

  it('does not mangle the raw original, angle brackets and all', () => {
    const out = roundTrip(SERVER_DOC);
    const block = out.content![1];
    expect(block.attrs?.[PRESERVED_ATTRS.raw]).toBe(RAW_MACRO);
  });

  it('survives an edit around the preserved content', () => {
    // The ADR-0012 scenario in miniature: somebody opens an imported page and
    // fixes a typo. Everything they did not touch must come back identical.
    const edited: CodexDoc = {
      ...SERVER_DOC,
      content: [
        { type: 'paragraph', content: [{ type: 'text', text: 'BEFORE, edited' }] },
        ...SERVER_DOC.content!.slice(1),
        { type: 'paragraph', content: [{ type: 'text', text: 'a new trailing paragraph' }] },
      ],
    };
    const out = roundTrip(edited);
    expect(preservedIdsIn(out)).toEqual(['u1', 'u2', 'u3']);
    expect(out.content![1]).toEqual(SERVER_DOC.content![1]);
    expect(out.content![2]).toEqual(SERVER_DOC.content![2]);
  });

  it('keeps a duplicated placeholder, which the server resolves to the same original', () => {
    // Copy-pasting an inert block is a reasonable thing for an author to do,
    // and Restore handles it: "A placeholder duplicated by the author restores
    // the same original at both positions."
    const duplicated: CodexDoc = {
      type: 'doc',
      content: [SERVER_DOC.content![1], SERVER_DOC.content![1]],
    };
    expect(preservedIdsIn(roundTrip(duplicated))).toEqual(['u1', 'u1']);
  });

  it('drops a genuinely unknown node — which is why the carriers must exist', () => {
    // The negative control. Without this, every assertion above could be
    // passing on a pipeline that has nothing to lose. ProseMirror discards a
    // node type outside its schema silently, and this is that behaviour,
    // demonstrated: `ac:structured-macro` as itself does not survive, while
    // the same content wrapped in a carrier (above) does.
    const unshielded: CodexDoc = {
      type: 'doc',
      content: [
        { type: 'paragraph', content: [{ type: 'text', text: 'kept' }] },
        { type: 'ac:structured-macro', attrs: { 'ac:name': 'gliffy' } },
      ],
    };
    // ProseMirror refuses an unknown type outright when parsing JSON, which is
    // the loud half of the behaviour; the silent half is what happens to
    // content it parses through a DOM paste. Either way the node does not
    // reach the document.
    expect(() => roundTrip(unshielded)).toThrow();
  });
});

describe('the schema keeps preserved content inert', () => {
  it('gives the block carrier no content model to type into', () => {
    expect(schema.nodes[NODE_UNKNOWN_CONTENT].isLeaf).toBe(true);
    expect(schema.nodes[NODE_UNKNOWN_INLINE].isLeaf).toBe(true);
  });

  it('does not let the unknown mark span across a split', () => {
    // A mark split in two would hand the server two placeholders claiming the
    // same captured original.
    expect(schema.marks[MARK_UNKNOWN_MARK].spec.spanning).toBe(false);
  });
});
