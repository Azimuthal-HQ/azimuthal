/**
 * The Codex editor's extension set — the editor's half of ADR-0012's safety
 * boundary.
 *
 * **The registered vocabulary must equal `schema.json`, in both directions.**
 * `web/src/lib/codex/schema.test.ts` compares the TypeScript mirror against
 * the Go manifest; `extensions.test.ts` compares a real ProseMirror schema
 * built from this list against that same mirror. So a type registered but not
 * listed, or listed but not registered, fails a test rather than shipping.
 *
 * The asymmetry is worth restating, because it decides what a mistake costs.
 * A type the editor registers but Go omits from the schema is merely captured
 * when it did not need to be: the author sees an inert block where a real one
 * was possible. Annoying, and safe. A type Go omits from *capture* because the
 * schema says the editor handles it, when the editor does not, is silent data
 * loss — ProseMirror drops it on load, before anything server-side can notice.
 *
 * That is why StarterKit is configured rather than taken as it comes. It ships
 * `underline`, which is not in `schema.json`: registering it would let an
 * author apply a mark the server never agreed to preserve, and the first save
 * would drop it without a word.
 */
import { getSchema } from '@tiptap/core';
import CodeBlockLowlight from '@tiptap/extension-code-block-lowlight';
import { Image } from '@tiptap/extension-image';
import { Link } from '@tiptap/extension-link';
import { TaskItem, TaskList } from '@tiptap/extension-list';
import { TableKit } from '@tiptap/extension-table';
import { ReactNodeViewRenderer } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import type { AnyExtension } from '@tiptap/core';

import { lowlight } from '../lowlight';
import { CodeBlockView } from '../nodeviews/CodeBlockView';
import { ImageView } from '../nodeviews/ImageView';
import { LINK_ATTRS } from '../../../lib/codex/schema';
import { macroExtensions } from './macros';
import { preservationExtensions } from './preservation';
import { InlineTag } from './tags';
import { wikilinkExtension } from './wikilinks';
import type { WikilinkOptions } from './wikilinks';

/**
 * The image node, extended with the attribute the document model addresses an
 * uploaded image by.
 *
 * `attachment_id` is snake_case because it crosses the wire into stored
 * content, and it is the name `doc.ImageAttachmentIDs` looks for when publish
 * verifies that every image on a page really is an image *on that page*. A
 * different spelling here would make an uploaded image invisible to that
 * check — which sounds permissive and is the opposite: the id would never be
 * collected, so the reference would never be validated at all.
 */
export const CodexImage = Image.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      attachment_id: {
        default: null,
        parseHTML: (el: HTMLElement) => el.getAttribute('data-attachment-id'),
        renderHTML: (attrs: Record<string, unknown>) =>
          attrs.attachment_id == null ? {} : { 'data-attachment-id': String(attrs.attachment_id) },
      },
    };
  },
  addNodeView() {
    return ReactNodeViewRenderer(ImageView);
  },
});

/**
 * The code block, highlighted, with a language picker. Replaces StarterKit's
 * plain `codeBlock` under the same node name, so the vocabulary is unchanged.
 */
export const CodexCodeBlock = CodeBlockLowlight.extend({
  addNodeView() {
    return ReactNodeViewRenderer(CodeBlockView);
  },
}).configure({ lowlight });

/**
 * The link mark, extended to carry an internal page reference.
 *
 * `linkTarget` in `internal/core/wiki/doc/text.go` prefers `href` and falls
 * back to `page_id`, emitting `page:<id>`. A page's URL depends on the space
 * it is read in, so a document must not bake one in — an internal link sets
 * `page_id` and leaves `href` null, and the reading surface turns it into a
 * route.
 */
export const CodexLink = Link.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      [LINK_ATTRS.pageId]: {
        default: null,
        parseHTML: (el: HTMLElement) => el.getAttribute('data-page-id'),
        renderHTML: (attrs: Record<string, unknown>) =>
          attrs[LINK_ATTRS.pageId] == null
            ? {}
            : { 'data-page-id': String(attrs[LINK_ATTRS.pageId]) },
      },
      /**
       * The third state: an UNRESOLVED wikilink naming a page that does not
       * exist yet. Not a variant of the other two — a link with an `href`
       * leaves Azimuthal, one with a `page_id` resolves to a page, and this one
       * has no destination at all until somebody creates the page.
       *
       * `data-target-title` is what the reading surface keys the create-on-click
       * offer off, and `data-unresolved` is what the stylesheet keys the dashed,
       * dimmed rendering off — a class would be lost the moment the mark was
       * re-rendered from its attributes.
       */
      [LINK_ATTRS.targetTitle]: {
        default: null,
        parseHTML: (el: HTMLElement) => el.getAttribute('data-target-title'),
        renderHTML: (attrs: Record<string, unknown>) =>
          attrs[LINK_ATTRS.targetTitle] == null
            ? {}
            : {
                'data-target-title': String(attrs[LINK_ATTRS.targetTitle]),
                'data-unresolved': 'true',
              },
      },
    };
  },
}).configure({
  openOnClick: false,
  autolink: true,
  // An href the editor accepts must not be able to execute script.
  protocols: ['http', 'https', 'mailto'],
  HTMLAttributes: { rel: 'noopener noreferrer nofollow' },
});

/**
 * The editor's extensions.
 *
 * A function rather than a constant because `TableKit`'s resize plugin and the
 * node views hold per-editor state; two editors sharing one instance is a
 * defect that only shows up when both are mounted at once — which the reading
 * surface and the editor do during a publish.
 */
export function codexExtensions(options: CodexExtensionOptions = {}): AnyExtension[] {
  return [
    StarterKit.configure({
      // Replaced below by the highlighted, language-picking variant.
      codeBlock: false,
      // Replaced below by the variant that carries an internal page reference.
      link: false,
      // NOT in schema.json. See the note at the top of this file.
      underline: false,
    }),
    CodexCodeBlock,
    CodexLink,
    TaskList,
    TaskItem.configure({ nested: true }),
    TableKit.configure({ table: { resizable: true } }),
    CodexImage,
    InlineTag,
    // Registers no node or mark type of its own — `[[…]]` produces the link
    // mark and `![[…]]` the pageInclude macro — so it is safe to include
    // unconditionally, including in the reading surface and in the schema drift
    // guard, both of which pass no options at all.
    wikilinkExtension(options.wikilinks),
    ...macroExtensions,
    ...preservationExtensions,
  ];
}

/**
 * What a caller can hand the extension list.
 *
 * Only the editor passes anything. The reading surface builds the same list
 * deliberately — a reader must see exactly what the author saw — and needs none
 * of it, because input rules are inert when the editor is not editable.
 */
export interface CodexExtensionOptions {
  wikilinks?: Partial<WikilinkOptions>;
}

/**
 * The node and mark types an extension list actually registers.
 *
 * Built by asking ProseMirror for the real schema rather than by walking the
 * extension objects, because that is the same construction the editor
 * performs — a list that agreed with a hand-rolled walk but not with the
 * schema would prove nothing.
 */
export function registeredTypes(extensions: AnyExtension[]): { nodes: string[]; marks: string[] } {
  const schema = getSchema(extensions);
  return {
    nodes: Object.keys(schema.nodes).sort(),
    marks: Object.keys(schema.marks).sort(),
  };
}
