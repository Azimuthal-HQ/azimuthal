/**
 * The three preservation carriers (ADR-0012, and D51 in
 * `docs/design/spec-repo-reconciliation.md`).
 *
 * ProseMirror silently drops content that does not match its schema. The
 * server therefore rewrites every node and mark type outside `schema.json`
 * into one of these three placeholders before the document reaches the
 * browser, and splices the verbatim originals back on publish. Registering
 * them is what makes that possible: a placeholder type the editor does not
 * define is dropped on load, exactly like the content it was standing in for,
 * and the whole mechanism silently becomes a no-op.
 *
 * A node type cannot be both block and inline, hence three carriers rather
 * than the one primitive ADR-0012 names — see D51, which is flagged for a
 * maintainer and not resolved here.
 *
 * Every one of them is an **atom**: inert, selectable and deletable as a unit,
 * never editable in place. Editing inside a placeholder would produce a
 * document whose displayed body no longer matches the bytes the server will
 * write, which is a lie about what is stored.
 *
 * `az_raw` is a display copy only. The server resolves the real original from
 * its own captured map and ignores whatever comes back, so nothing here can
 * corrupt storage. The worst this file can do is lose a placeholder — and that
 * is reported by publish, and refused.
 */
import { Mark, Node, mergeAttributes } from '@tiptap/core';
import { ReactNodeViewRenderer } from '@tiptap/react';

import {
  MARK_UNKNOWN_MARK,
  NODE_UNKNOWN_CONTENT,
  NODE_UNKNOWN_INLINE,
  PRESERVED_ATTRS,
} from '../../../lib/codex/schema';
import { UnknownContentView } from '../nodeviews/UnknownContentView';
import { UnknownInlineView } from '../nodeviews/UnknownInlineView';

/**
 * The placeholder attribute set, declared identically on all three carriers.
 *
 * Declared with `default: null` and rendered into `data-*` attributes so a
 * placeholder survives a copy/paste round trip through the DOM as well as a
 * JSON one. Duplicating an inert block is a reasonable thing for an author to
 * do, and the server restores the same original at both positions.
 */
function preservedAttributes() {
  const attrs: Record<string, unknown> = {};
  for (const name of Object.values(PRESERVED_ATTRS)) {
    attrs[name] = {
      default: null,
      parseHTML: (element: HTMLElement) => element.getAttribute(`data-${name}`),
      renderHTML: (attributes: Record<string, unknown>) => {
        const value = attributes[name];
        return value == null ? {} : { [`data-${name}`]: String(value) };
      },
    };
  }
  return attrs;
}

/** The block-level placeholder (ADR-0012 section 1). */
export const UnknownContent = Node.create({
  name: NODE_UNKNOWN_CONTENT,
  group: 'block',
  atom: true,
  selectable: true,
  draggable: true,

  addAttributes: preservedAttributes,

  parseHTML() {
    return [{ tag: `div[data-codex-preserved="${NODE_UNKNOWN_CONTENT}"]` }];
  },

  renderHTML({ HTMLAttributes }) {
    return [
      'div',
      mergeAttributes(HTMLAttributes, { 'data-codex-preserved': NODE_UNKNOWN_CONTENT }),
    ];
  },

  addNodeView() {
    return ReactNodeViewRenderer(UnknownContentView);
  },
});

/** The inline placeholder. */
export const UnknownInline = Node.create({
  name: NODE_UNKNOWN_INLINE,
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,

  addAttributes: preservedAttributes,

  parseHTML() {
    return [{ tag: `span[data-codex-preserved="${NODE_UNKNOWN_INLINE}"]` }];
  },

  renderHTML({ HTMLAttributes }) {
    return [
      'span',
      mergeAttributes(HTMLAttributes, { 'data-codex-preserved': NODE_UNKNOWN_INLINE }),
    ];
  },

  addNodeView() {
    return ReactNodeViewRenderer(UnknownInlineView, { as: 'span' });
  },
});

/**
 * The mark placeholder.
 *
 * A mark has no node view, so it is styled rather than replaced: the text it
 * covers stays readable and editable, because unlike an unknown node an
 * unknown mark's *content* is ordinary text this editor understands perfectly
 * well. Only the formatting is unrepresentable. Codex's markdown editor wrote
 * text colour and highlight as inline HTML before it was removed in PR #75, so
 * this is the carrier real pages in this repository already need. ADR-0012
 * names it explicitly as of the S1 amendment.
 */
export const UnknownMark = Mark.create({
  name: MARK_UNKNOWN_MARK,
  // Keep the mark whole: splitting one across an edit would hand the server
  // two placeholders claiming the same captured original.
  spanning: false,
  inclusive: false,

  addAttributes: preservedAttributes,

  parseHTML() {
    return [{ tag: `span[data-codex-preserved="${MARK_UNKNOWN_MARK}"]` }];
  },

  renderHTML({ mark, HTMLAttributes }) {
    return [
      'span',
      mergeAttributes(HTMLAttributes, {
        'data-codex-preserved': MARK_UNKNOWN_MARK,
        class: 'codex-unknown-mark',
        // The label is the only thing that tells a reader this formatting was
        // preserved rather than rendered. ADR-0012: visibly preserved, never
        // silently approximated.
        //
        // Read from `mark.attrs`, not from `HTMLAttributes` — by this point
        // the latter holds the *rendered* form (`data-az_name`), so looking
        // the attribute up by its own name there always missed and every
        // preserved mark was labelled "unknown".
        title: `Preserved formatting: ${mark.attrs[PRESERVED_ATTRS.name] ?? 'unknown'}`,
      }),
      0,
    ];
  },
});

export const preservationExtensions = [UnknownContent, UnknownInline, UnknownMark];
