/**
 * The Codex document schema vocabulary — the editor's half of the list the Go
 * layer preserves against.
 *
 * This file MIRRORS `internal/core/wiki/doc/schema.json`, and `schema.test.ts`
 * fails on any difference in either direction. It is a hand-written mirror
 * rather than an import because Vite cannot bundle a file from outside `web/`,
 * while a test running in Node can read it perfectly well — so the check is a
 * test rather than a build step.
 *
 * Why the drift matters, and why it is not symmetric (ADR-0012):
 *
 * - A type the editor registers but Go omits is merely captured when it did not
 *   need to be. The author sees an inert block where they could have had a real
 *   one. Annoying, and safe.
 * - A type Go omits from capture because it believes the editor handles it, when
 *   the editor does not, is **silent data loss**. ProseMirror drops content
 *   outside its schema without a word, and the drop happens on load, before
 *   anything server-side can notice.
 *
 * So: every name here must be registered by the editor, and every name the
 * editor registers must be here.
 */

/** Node types the editor's schema defines. */
export const CODEX_NODES = [
  // Core rich text (issue #15 E2).
  'doc',
  'paragraph',
  'text',
  'hardBreak',
  'heading',
  'bulletList',
  'orderedList',
  'listItem',
  'taskList',
  'taskItem',
  'blockquote',
  'codeBlock',
  'horizontalRule',
  'table',
  'tableRow',
  'tableHeader',
  'tableCell',
  'image',

  // ADR-0012 section 4 first-class macros.
  'panel',
  'expand',
  'statusLozenge',
  'tableOfContents',
  'layout',
  'layoutColumn',
  'childrenDisplay',
  'pageInclude',

  // The inline `#tag` token. Core rather than macro: it is ordinary authored
  // content, not an imported Confluence construct.
  'inlineTag',

  // ADR-0012's zero-silent-data-loss machinery.
  'unknownContent',
  'unknownInline',
] as const;

/** Mark types the editor's schema defines. */
export const CODEX_MARKS = [
  'bold',
  'italic',
  'strike',
  'code',
  'link',

  'unknownMark',
] as const;

/**
 * Node types that are themselves inline, and node types whose children are
 * inline.
 *
 * These exist so the server can choose between the block and the inline
 * preservation placeholder for a type it has never seen — it decides from the
 * position, because it cannot ask the type. Getting one wrong is not silent: a
 * placeholder that lands where ProseMirror will not accept it is dropped, the
 * server sees the id go missing, and publish refuses the write.
 */
export const CODEX_INLINE_NODES = [
  'text',
  'hardBreak',
  'statusLozenge',
  'inlineTag',
  'unknownInline',
] as const;

export const CODEX_INLINE_CONTENT_NODES = ['paragraph', 'heading', 'codeBlock'] as const;

export type CodexNodeName = (typeof CODEX_NODES)[number];
export type CodexMarkName = (typeof CODEX_MARKS)[number];

/** The three preservation carriers, named so nothing has to spell them twice. */
export const NODE_UNKNOWN_CONTENT = 'unknownContent';
export const NODE_UNKNOWN_INLINE = 'unknownInline';
export const MARK_UNKNOWN_MARK = 'unknownMark';

/** The inline tag token, and the link mark, named for the same reason. */
export const NODE_INLINE_TAG = 'inlineTag';
export const MARK_LINK = 'link';

/**
 * The attributes the server's markdown projection reads by name.
 *
 * Mirrors `projectedAttrs` in `internal/core/wiki/doc/schema.json`, and
 * `schema.test.ts` fails on any difference in either direction — the same
 * contract the type lists carry, guarding a different failure.
 *
 * A TYPE that drifts fails loudly in one direction and destroys content in the
 * other. An ATTRIBUTE that drifts does neither: the page publishes, the document
 * stores correctly, and the content quietly stops being findable, because
 * `internal/core/wiki/doc/text.go` looked for a name that is no longer there.
 * Nothing announces it. That is why the list is data checked by a test rather
 * than a comment asking people to remember.
 *
 * `extensions.test.ts` then asserts the real ProseMirror schema declares every
 * attribute named here, which is the half a list on its own cannot promise.
 */
export const PROJECTED_ATTRS = {
  nodes: {
    heading: ['level'],
    codeBlock: ['language'],
    image: ['attachment_id'],
    panel: ['kind'],
    expand: ['title'],
    statusLozenge: ['text'],
    pageInclude: ['page_id'],
    inlineTag: ['label'],
  },
  marks: {
    link: ['href', 'page_id', 'target_title'],
  },
} as const;

/**
 * The attribute names on the link mark, and the three states a link can be in.
 *
 * They are states, not variants — see `AttrLinkTargetTitle` in
 * `internal/core/wiki/doc/schema.go`:
 *
 * - `href` set: an external link, which leaves Azimuthal.
 * - `page_id` set: a resolved internal link. Never a URL, because a page's URL
 *   depends on the space it is being read in.
 * - `target_title` set: an UNRESOLVED wikilink — the author named a page that
 *   does not exist yet. Clicking it offers to create one.
 */
export const LINK_ATTRS = {
  href: 'href',
  pageId: 'page_id',
  targetTitle: 'target_title',
} as const;

/** The inline tag's one attribute: the label as the author typed it. */
export const TAG_ATTRS = { label: 'label' } as const;

/**
 * Attribute names on a preservation placeholder. Prefixed so they can never
 * collide with an attribute of the original node they describe.
 *
 * `az_raw` is a display copy: the server resolves the real original from its own
 * captured map and ignores whatever comes back here, so altering it changes
 * nothing about what gets stored. It is carried so the editor can label and size
 * the block, and so a reader can be shown what is being preserved.
 */
export const PRESERVED_ATTRS = {
  id: 'az_id',
  name: 'az_name',
  source: 'az_source',
  raw: 'az_raw',
  text: 'az_text',
} as const;

/** A ProseMirror document as it crosses the wire. */
export interface CodexDoc {
  type: 'doc';
  content?: CodexNode[];
}

export interface CodexNode {
  type: string;
  attrs?: Record<string, unknown>;
  content?: CodexNode[];
  marks?: CodexMark[];
  text?: string;
}

export interface CodexMark {
  type: string;
  attrs?: Record<string, unknown>;
}

/** The empty document — what a page with no content holds. */
export function emptyCodexDoc(): CodexDoc {
  return { type: 'doc', content: [] };
}

/**
 * Collects the preservation ids in a document, in document order.
 *
 * The editor takes this snapshot from the SERVER'S payload, before ProseMirror
 * parses it — which is the whole point. If ProseMirror drops a placeholder on
 * load, the id is in this snapshot and absent from the editor's state, so the
 * loss is detectable rather than invisible. Publish sends the difference as
 * `acknowledged_lost_ids` only for blocks the author actually deleted; anything
 * else missing is refused by the server.
 */
export function preservedIdsIn(node: CodexNode | CodexDoc | null | undefined): string[] {
  const ids: string[] = [];
  walk(node, (n) => {
    if (
      n.type === NODE_UNKNOWN_CONTENT ||
      n.type === NODE_UNKNOWN_INLINE ||
      n.type === MARK_UNKNOWN_MARK
    ) {
      const id = n.attrs?.[PRESERVED_ATTRS.id];
      if (typeof id === 'string' && id !== '') ids.push(id);
    }
  });
  return ids;
}

function walk(
  node: CodexNode | CodexDoc | null | undefined,
  visit: (n: CodexNode) => void,
): void {
  if (!node || typeof node !== 'object') return;
  const n = node as CodexNode;
  visit(n);
  n.marks?.forEach((mark) => walk(mark as CodexNode, visit));
  n.content?.forEach((child) => walk(child, visit));
}
