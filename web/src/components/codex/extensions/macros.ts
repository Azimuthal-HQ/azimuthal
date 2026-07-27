/**
 * The eight first-class macros (ADR-0012 section 4).
 *
 * **The attribute names here are a contract, not a local choice.** The server
 * projects every published document to markdown for the generated
 * `search_vector` (migration 036), and that projection reads specific
 * attributes by name — `internal/core/wiki/doc/text.go`:
 *
 * | node              | attribute read | what is lost if it is named differently |
 * |-------------------|----------------|------------------------------------------|
 * | `panel`           | `kind`         | the panel's kind, silently defaulted to "info" |
 * | `expand`          | `title`        | the summary line, which is often the only prose |
 * | `statusLozenge`   | `text`         | the label — the whole content of the node |
 * | `pageInclude`     | `page_id`      | which page was included |
 * | `codeBlock`       | `language`     | the fence's language |
 *
 * Nothing fails loudly if one of these is renamed: the page publishes, the
 * document stores correctly, and the content quietly stops being findable.
 * That is why they are written down here rather than inferred.
 *
 * `layout`, `layoutColumn`, `tableOfContents` and `childrenDisplay` have no
 * attributes the projection reads, so theirs are the editor's own.
 *
 * Two macro groups ADR-0012 names are deliberately absent, as they were in
 * PR #73: a Vector/Beacon item embed (a cross-space route-shape question
 * ADR-0010 governs) and the dynamic-content macros (which map onto the
 * saved-views layer, and that is P4).
 */
import { Node, mergeAttributes } from '@tiptap/core';
import { ReactNodeViewRenderer } from '@tiptap/react';

import { ChildrenDisplayView } from '../nodeviews/ChildrenDisplayView';
import { ExpandView } from '../nodeviews/ExpandView';
import { LayoutView } from '../nodeviews/LayoutView';
import { PageIncludeView } from '../nodeviews/PageIncludeView';
import { PanelView } from '../nodeviews/PanelView';
import { StatusLozengeView } from '../nodeviews/StatusLozengeView';
import { TableOfContentsView } from '../nodeviews/TableOfContentsView';

/** The panel kinds, and the only values `kind` ever takes. */
export const PANEL_KINDS = ['info', 'note', 'success', 'warning', 'error'] as const;
export type PanelKind = (typeof PANEL_KINDS)[number];

/** The lozenge colours. `text` is the label; `color` is presentation only. */
export const LOZENGE_COLORS = ['neutral', 'blue', 'green', 'yellow', 'red', 'purple'] as const;
export type LozengeColor = (typeof LOZENGE_COLORS)[number];

declare module '@tiptap/core' {
  interface Commands<ReturnType> {
    codexMacros: {
      setPanel: (kind?: PanelKind) => ReturnType;
      setExpand: (title?: string) => ReturnType;
      insertStatusLozenge: (attrs?: { text?: string; color?: LozengeColor }) => ReturnType;
      insertTableOfContents: () => ReturnType;
      insertLayout: (columns?: number) => ReturnType;
      insertChildrenDisplay: () => ReturnType;
      insertPageInclude: (pageId: string) => ReturnType;
    };
  }
}

/** An admonition. `kind` is read by the markdown projection. */
export const Panel = Node.create({
  name: 'panel',
  group: 'block',
  content: 'block+',
  defining: true,

  addAttributes() {
    return {
      kind: {
        default: 'info' as PanelKind,
        parseHTML: (el: HTMLElement) => el.getAttribute('data-kind') ?? 'info',
        renderHTML: (attrs: Record<string, unknown>) => ({ 'data-kind': String(attrs.kind ?? 'info') }),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'div[data-codex-macro="panel"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes(HTMLAttributes, { 'data-codex-macro': 'panel' }), 0];
  },

  addNodeView() {
    return ReactNodeViewRenderer(PanelView);
  },

  addCommands() {
    return {
      setPanel:
        (kind: PanelKind = 'info') =>
        ({ commands }) =>
          commands.wrapIn(this.name, { kind }),
    };
  },
});

/** A collapsible section. `title` is read by the markdown projection. */
export const Expand = Node.create({
  name: 'expand',
  group: 'block',
  content: 'block+',
  defining: true,

  addAttributes() {
    return {
      title: {
        default: '',
        parseHTML: (el: HTMLElement) => el.getAttribute('data-title') ?? '',
        renderHTML: (attrs: Record<string, unknown>) => ({ 'data-title': String(attrs.title ?? '') }),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'div[data-codex-macro="expand"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes(HTMLAttributes, { 'data-codex-macro': 'expand' }), 0];
  },

  addNodeView() {
    return ReactNodeViewRenderer(ExpandView);
  },

  addCommands() {
    return {
      setExpand:
        (title = '') =>
        ({ commands }) =>
          commands.wrapIn(this.name, { title }),
    };
  },
});

/**
 * An inline status pill. `text` is read by the markdown projection — it is the
 * whole content of the node, so a rename would delete the lozenge from search.
 */
export const StatusLozenge = Node.create({
  name: 'statusLozenge',
  group: 'inline',
  inline: true,
  atom: true,
  selectable: true,

  addAttributes() {
    return {
      text: {
        default: 'STATUS',
        parseHTML: (el: HTMLElement) => el.getAttribute('data-text') ?? '',
        renderHTML: (attrs: Record<string, unknown>) => ({ 'data-text': String(attrs.text ?? '') }),
      },
      color: {
        default: 'neutral' as LozengeColor,
        parseHTML: (el: HTMLElement) => el.getAttribute('data-color') ?? 'neutral',
        renderHTML: (attrs: Record<string, unknown>) => ({ 'data-color': String(attrs.color ?? 'neutral') }),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'span[data-codex-macro="statusLozenge"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['span', mergeAttributes(HTMLAttributes, { 'data-codex-macro': 'statusLozenge' })];
  },

  addNodeView() {
    return ReactNodeViewRenderer(StatusLozengeView, { as: 'span' });
  },

  addCommands() {
    return {
      insertStatusLozenge:
        (attrs = {}) =>
        ({ commands }) =>
          commands.insertContent({
            type: this.name,
            attrs: { text: attrs.text ?? 'STATUS', color: attrs.color ?? 'neutral' },
          }),
    };
  },
});

/** A table of contents, rendered from the document's own headings. */
export const TableOfContents = Node.create({
  name: 'tableOfContents',
  group: 'block',
  atom: true,
  selectable: true,

  addAttributes() {
    return {
      maxLevel: {
        default: 3,
        parseHTML: (el: HTMLElement) => Number(el.getAttribute('data-max-level') ?? 3),
        renderHTML: (attrs: Record<string, unknown>) => ({ 'data-max-level': String(attrs.maxLevel ?? 3) }),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'div[data-codex-macro="tableOfContents"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes(HTMLAttributes, { 'data-codex-macro': 'tableOfContents' })];
  },

  addNodeView() {
    return ReactNodeViewRenderer(TableOfContentsView);
  },

  addCommands() {
    return {
      insertTableOfContents:
        () =>
        ({ commands }) =>
          commands.insertContent({ type: this.name }),
    };
  },
});

/** A multi-column section. Holds columns and nothing else. */
export const Layout = Node.create({
  name: 'layout',
  group: 'block',
  content: 'layoutColumn+',
  defining: true,
  isolating: true,

  parseHTML() {
    return [{ tag: 'div[data-codex-macro="layout"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes(HTMLAttributes, { 'data-codex-macro': 'layout' }), 0];
  },

  addNodeView() {
    return ReactNodeViewRenderer(LayoutView);
  },

  addCommands() {
    return {
      insertLayout:
        (columns = 2) =>
        ({ commands }) =>
          commands.insertContent({
            type: this.name,
            content: Array.from({ length: Math.max(2, Math.min(3, columns)) }, () => ({
              type: 'layoutColumn',
              content: [{ type: 'paragraph' }],
            })),
          }),
    };
  },
});

/** One column of a layout. */
export const LayoutColumn = Node.create({
  name: 'layoutColumn',
  content: 'block+',
  isolating: true,

  parseHTML() {
    return [{ tag: 'div[data-codex-macro="layoutColumn"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return [
      'div',
      mergeAttributes(HTMLAttributes, {
        'data-codex-macro': 'layoutColumn',
        class: 'codex-layout-column',
      }),
      0,
    ];
  },
});

/** A list of this page's child pages, resolved against the wiki tree. */
export const ChildrenDisplay = Node.create({
  name: 'childrenDisplay',
  group: 'block',
  atom: true,
  selectable: true,

  addAttributes() {
    return {
      depth: {
        default: 1,
        parseHTML: (el: HTMLElement) => Number(el.getAttribute('data-depth') ?? 1),
        renderHTML: (attrs: Record<string, unknown>) => ({ 'data-depth': String(attrs.depth ?? 1) }),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'div[data-codex-macro="childrenDisplay"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes(HTMLAttributes, { 'data-codex-macro': 'childrenDisplay' })];
  },

  addNodeView() {
    return ReactNodeViewRenderer(ChildrenDisplayView);
  },

  addCommands() {
    return {
      insertChildrenDisplay:
        () =>
        ({ commands }) =>
          commands.insertContent({ type: this.name }),
    };
  },
});

/**
 * A reference to another page. `page_id` is read by the markdown projection.
 *
 * Snake_case, unlike this file's other attributes, because it crosses the wire
 * into stored content and the wire format is lowercase snake_case without
 * exception (CLAUDE.md section 1). The same reasoning applies to the `link`
 * mark's `page_id`.
 *
 * No title is cached alongside it: a copy would go stale the moment the target
 * page is renamed, and the reading surface has the page list already.
 */
export const PageInclude = Node.create({
  name: 'pageInclude',
  group: 'block',
  atom: true,
  selectable: true,

  addAttributes() {
    return {
      page_id: {
        default: '',
        parseHTML: (el: HTMLElement) => el.getAttribute('data-page-id') ?? '',
        renderHTML: (attrs: Record<string, unknown>) => ({ 'data-page-id': String(attrs.page_id ?? '') }),
      },
    };
  },

  parseHTML() {
    return [{ tag: 'div[data-codex-macro="pageInclude"]' }];
  },

  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes(HTMLAttributes, { 'data-codex-macro': 'pageInclude' })];
  },

  addNodeView() {
    return ReactNodeViewRenderer(PageIncludeView);
  },

  addCommands() {
    return {
      insertPageInclude:
        (pageId: string) =>
        ({ commands }) =>
          commands.insertContent({ type: this.name, attrs: { page_id: pageId } }),
    };
  },
});

export const macroExtensions = [
  Panel,
  Expand,
  StatusLozenge,
  TableOfContents,
  Layout,
  LayoutColumn,
  ChildrenDisplay,
  PageInclude,
];
