/**
 * The editor toolbar.
 *
 * Keyboard-accessible in the ARIA sense rather than merely tabbable: it is a
 * single tab stop with arrow-key navigation between its controls
 * (`role="toolbar"` + roving tabindex). Twenty-odd buttons each taking their
 * own tab stop would put the editor's content two dozen presses away from the
 * page's other controls, which is the failure mode a toolbar role exists to
 * avoid.
 *
 * Every toggle reports its state with `aria-pressed`, so a screen reader says
 * "bold, pressed" rather than leaving the active styling as the only signal.
 */
import { useEditorState } from '@tiptap/react';
import type { Editor } from '@tiptap/react';
import {
  Bold,
  ChevronDown,
  Code,
  Code2,
  Columns2,
  Columns3,
  Columns as ColumnsIcon,
  Heading,
  Heading1,
  Heading2,
  Heading3,
  Image as ImageIcon,
  Italic,
  Link2,
  Link2Off,
  List,
  ListChecks,
  ListOrdered,
  ListTree,
  Minus,
  Network,
  Quote,
  Rows as RowsIcon,
  Rows3,
  Square,
  Strikethrough,
  Table as TableIcon,
  Tag,
  Trash2,
} from 'lucide-react';
import { useCallback, useRef, useState } from 'react';
import type { ComponentType, ReactNode } from 'react';

import { PagePicker } from './PagePicker';

interface CodexToolbarProps {
  editor: Editor;
  /** Opens the file chooser for an image upload. */
  onInsertImage: () => void;
  /** True while an image upload is in flight. */
  uploadingImage?: boolean;
}

interface ToolbarItem {
  key: string;
  label: string;
  icon: ComponentType<{ className?: string }>;
  run: () => void;
  active?: boolean;
  disabled?: boolean;
}

export function CodexToolbar({ editor, onInsertImage, uploadingImage }: CodexToolbarProps) {
  const [linkPickerOpen, setLinkPickerOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  // One subscription for every active-state the toolbar shows. Reading
  // editor.isActive() directly in render would not re-run when the selection
  // moves, so the buttons would show the state they had when the document last
  // changed.
  const state = useEditorState({
    editor,
    selector: ({ editor: e }) => ({
      bold: e.isActive('bold'),
      italic: e.isActive('italic'),
      strike: e.isActive('strike'),
      code: e.isActive('code'),
      link: e.isActive('link'),
      h1: e.isActive('heading', { level: 1 }),
      h2: e.isActive('heading', { level: 2 }),
      h3: e.isActive('heading', { level: 3 }),
      bulletList: e.isActive('bulletList'),
      orderedList: e.isActive('orderedList'),
      taskList: e.isActive('taskList'),
      blockquote: e.isActive('blockquote'),
      codeBlock: e.isActive('codeBlock'),
      panel: e.isActive('panel'),
      inTable: e.isActive('table'),
    }),
  });

  const groups: { name: string; items: ToolbarItem[] }[] = [
    {
      name: 'Headings',
      items: [
        {
          key: 'h1',
          label: 'Heading 1',
          icon: Heading1,
          active: state.h1,
          run: () => editor.chain().focus().toggleHeading({ level: 1 }).run(),
        },
        {
          key: 'h2',
          label: 'Heading 2',
          icon: Heading2,
          active: state.h2,
          run: () => editor.chain().focus().toggleHeading({ level: 2 }).run(),
        },
        {
          key: 'h3',
          label: 'Heading 3',
          icon: Heading3,
          active: state.h3,
          run: () => editor.chain().focus().toggleHeading({ level: 3 }).run(),
        },
      ],
    },
    {
      name: 'Formatting',
      items: [
        {
          key: 'bold',
          label: 'Bold',
          icon: Bold,
          active: state.bold,
          run: () => editor.chain().focus().toggleBold().run(),
        },
        {
          key: 'italic',
          label: 'Italic',
          icon: Italic,
          active: state.italic,
          run: () => editor.chain().focus().toggleItalic().run(),
        },
        {
          key: 'strike',
          label: 'Strikethrough',
          icon: Strikethrough,
          active: state.strike,
          run: () => editor.chain().focus().toggleStrike().run(),
        },
        {
          key: 'code',
          label: 'Inline code',
          icon: Code,
          active: state.code,
          run: () => editor.chain().focus().toggleCode().run(),
        },
      ],
    },
    {
      name: 'Links',
      items: [
        {
          key: 'link',
          label: 'External link',
          icon: Link2,
          active: state.link,
          run: () => promptForExternalLink(editor),
        },
        {
          key: 'page-link',
          label: 'Link to a page',
          icon: Network,
          run: () => setLinkPickerOpen(true),
        },
        {
          key: 'unlink',
          label: 'Remove link',
          icon: Link2Off,
          disabled: !state.link,
          run: () => editor.chain().focus().unsetLink().run(),
        },
      ],
    },
    {
      name: 'Blocks',
      items: [
        {
          key: 'bullet',
          label: 'Bullet list',
          icon: List,
          active: state.bulletList,
          run: () => editor.chain().focus().toggleBulletList().run(),
        },
        {
          key: 'ordered',
          label: 'Numbered list',
          icon: ListOrdered,
          active: state.orderedList,
          run: () => editor.chain().focus().toggleOrderedList().run(),
        },
        {
          key: 'task',
          label: 'Task list',
          icon: ListChecks,
          active: state.taskList,
          run: () => editor.chain().focus().toggleTaskList().run(),
        },
        {
          key: 'quote',
          label: 'Blockquote',
          icon: Quote,
          active: state.blockquote,
          run: () => editor.chain().focus().toggleBlockquote().run(),
        },
        {
          key: 'codeblock',
          label: 'Code block',
          icon: Code2,
          active: state.codeBlock,
          run: () => editor.chain().focus().toggleCodeBlock().run(),
        },
        {
          key: 'rule',
          label: 'Horizontal rule',
          icon: Minus,
          run: () => editor.chain().focus().setHorizontalRule().run(),
        },
      ],
    },
    {
      name: 'Insert',
      items: [
        {
          key: 'table',
          label: 'Table',
          icon: TableIcon,
          run: () =>
            editor.chain().focus().insertTable({ rows: 3, cols: 3, withHeaderRow: true }).run(),
        },
        {
          key: 'image',
          label: uploadingImage ? 'Uploading image…' : 'Image',
          icon: ImageIcon,
          disabled: uploadingImage,
          run: onInsertImage,
        },
        {
          key: 'panel',
          label: 'Panel',
          icon: Square,
          active: state.panel,
          run: () => editor.chain().focus().setPanel('info').run(),
        },
        {
          key: 'expand',
          label: 'Expandable section',
          icon: ChevronDown,
          run: () => editor.chain().focus().setExpand('Details').run(),
        },
        {
          key: 'status',
          label: 'Status lozenge',
          icon: Tag,
          run: () => editor.chain().focus().insertStatusLozenge().run(),
        },
        {
          key: 'toc',
          label: 'Table of contents',
          icon: ListTree,
          run: () => editor.chain().focus().insertTableOfContents().run(),
        },
        {
          key: 'layout',
          label: 'Columns',
          icon: Columns3,
          run: () => editor.chain().focus().insertLayout(2).run(),
        },
        {
          key: 'children',
          label: 'Child pages',
          icon: Network,
          run: () => editor.chain().focus().insertChildrenDisplay().run(),
        },
      ],
    },
  ];

  // Table editing appears only inside a table. Six always-visible controls that
  // are disabled everywhere else would be six more stops to arrow past for the
  // majority of documents that contain no table at all.
  if (state.inTable) {
    groups.push({
      name: 'Table',
      items: [
        {
          key: 'row-after',
          label: 'Add row below',
          icon: Rows3,
          run: () => editor.chain().focus().addRowAfter().run(),
        },
        {
          key: 'row-delete',
          label: 'Delete row',
          icon: RowsIcon,
          run: () => editor.chain().focus().deleteRow().run(),
        },
        {
          key: 'col-after',
          label: 'Add column to the right',
          icon: Columns2,
          run: () => editor.chain().focus().addColumnAfter().run(),
        },
        {
          key: 'col-delete',
          label: 'Delete column',
          icon: ColumnsIcon,
          run: () => editor.chain().focus().deleteColumn().run(),
        },
        {
          key: 'header-row',
          label: 'Toggle header row',
          icon: Heading,
          run: () => editor.chain().focus().toggleHeaderRow().run(),
        },
        {
          key: 'table-delete',
          label: 'Delete table',
          icon: Trash2,
          run: () => editor.chain().focus().deleteTable().run(),
        },
      ],
    });
  }

  const flat = groups.flatMap((g) => g.items);

  /**
   * Roving tabindex: arrows move between controls, Home/End jump to the ends,
   * and the toolbar as a whole is one tab stop.
   */
  const onKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLDivElement>) => {
      const keys = ['ArrowRight', 'ArrowLeft', 'Home', 'End'];
      if (!keys.includes(event.key)) return;
      const buttons = Array.from(
        containerRef.current?.querySelectorAll<HTMLButtonElement>('button[data-toolbar-item]') ?? [],
      ).filter((b) => !b.disabled);
      if (buttons.length === 0) return;

      const current = buttons.indexOf(document.activeElement as HTMLButtonElement);
      let next = current;
      if (event.key === 'ArrowRight') next = current < 0 ? 0 : (current + 1) % buttons.length;
      if (event.key === 'ArrowLeft')
        next = current < 0 ? buttons.length - 1 : (current - 1 + buttons.length) % buttons.length;
      if (event.key === 'Home') next = 0;
      if (event.key === 'End') next = buttons.length - 1;

      event.preventDefault();
      buttons[next]?.focus();
    },
    [],
  );

  const firstEnabled = flat.find((item) => !item.disabled)?.key;

  return (
    <>
      <div
        ref={containerRef}
        role="toolbar"
        aria-label="Formatting"
        aria-orientation="horizontal"
        onKeyDown={onKeyDown}
        data-testid="codex-toolbar"
        className="flex flex-wrap items-center gap-0.5 border-b border-[var(--color-border)] bg-[var(--color-surface)] px-2 py-1.5"
      >
        {groups.map((group, groupIndex) => (
          <div key={group.name} className="flex items-center gap-0.5" role="group" aria-label={group.name}>
            {groupIndex > 0 && <Separator />}
            {group.items.map((item) => (
              <ToolbarButton
                key={item.key}
                item={item}
                tabIndex={item.key === firstEnabled ? 0 : -1}
              />
            ))}
          </div>
        ))}
      </div>

      {linkPickerOpen && (
        <PagePicker
          title="Link to a page"
          onSelect={(pageId) => {
            // page_id and no href: a page's URL depends on the space it is read
            // in, so the document stores the reference, not the address.
            editor.chain().focus().setLink({ href: '', page_id: pageId } as never).run();
            setLinkPickerOpen(false);
          }}
          onClose={() => setLinkPickerOpen(false)}
        />
      )}
    </>
  );
}

function Separator(): ReactNode {
  return <div aria-hidden="true" className="mx-1 h-4 w-px bg-[var(--color-border)]" />;
}

function ToolbarButton({ item, tabIndex }: { item: ToolbarItem; tabIndex: number }) {
  const Icon = item.icon;
  return (
    <button
      type="button"
      data-toolbar-item
      data-testid={`codex-tool-${item.key}`}
      tabIndex={tabIndex}
      title={item.label}
      aria-label={item.label}
      aria-pressed={item.active === undefined ? undefined : item.active}
      disabled={item.disabled}
      onMouseDown={(e) => e.preventDefault()}
      onClick={item.run}
      className={[
        'rounded-[var(--radius-md)] p-1.5 transition-colors disabled:opacity-30',
        'focus:outline-none focus-visible:ring-1 focus-visible:ring-[var(--module-codex)]',
        item.active
          ? 'bg-[color-mix(in_srgb,var(--module-codex)_22%,transparent)] text-[var(--module-codex)]'
          : 'text-[var(--color-text-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)]',
      ].join(' ')}
    >
      <Icon className="h-4 w-4" />
    </button>
  );
}

/**
 * Asks for an external URL.
 *
 * `window.prompt` rather than a bespoke dialog: it is keyboard-operable and
 * screen-reader-announced for free, and a link URL is one field. The link mark
 * restricts protocols, so a `javascript:` URL typed here is refused by the
 * extension rather than by this function.
 */
function promptForExternalLink(editor: Editor): void {
  const previous = String(editor.getAttributes('link').href ?? '');
  const url = window.prompt('Link URL', previous);
  if (url === null) return;
  if (url.trim() === '') {
    editor.chain().focus().unsetLink().run();
    return;
  }
  editor.chain().focus().setLink({ href: url.trim(), page_id: null } as never).run();
}
