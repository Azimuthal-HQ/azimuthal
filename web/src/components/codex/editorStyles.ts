/**
 * The document's typography and block styling.
 *
 * One string, shared by the editor and the reading surface, because they
 * render the same document through the same extensions — a second stylesheet
 * for reading is how "what you typed" and "what a reader sees" start to
 * diverge. Everything here is a shell token: no hard-coded hex, so the
 * document follows the theme instead of pinning itself to one.
 */
/**
 * The document's measure: fluid, with a clamp.
 *
 * One string for the reader, the editor and the drafts list, for the same
 * reason `editorSurfaceClasses` is one string — the reading view and the
 * editing view are the same document, and a width that differed between them
 * would mean every line broke somewhere else the moment you pressed Edit.
 * Before this, they differed as much as two surfaces can: the reader was pinned
 * to a fixed 76ch and the editor had no constraint at all.
 *
 * `w-full` with a `max-width` is what makes it fluid rather than fixed — the
 * element takes the width it is given and stops growing at the clamp, which is
 * a CSS property rather than a resize listener, so it reflows during a drag
 * with nothing subscribed to anything.
 *
 * The clamp itself is `--codex-measure` in `tokens.css`, which is where the
 * number and the reasoning for it live.
 */
export const codexMeasureClasses = 'mx-auto w-full max-w-[var(--codex-measure)]';

export const editorSurfaceClasses = [
  '[&_.ProseMirror]:outline-none',
  '[&_.ProseMirror]:text-[var(--color-text)]',
  '[&_.ProseMirror]:leading-[1.78]',

  // Headings
  '[&_.ProseMirror_h1]:text-[19px] [&_.ProseMirror_h1]:font-semibold [&_.ProseMirror_h1]:tracking-[-.01em] [&_.ProseMirror_h1]:mt-4 [&_.ProseMirror_h1]:mb-2',
  '[&_.ProseMirror_h2]:text-[16px] [&_.ProseMirror_h2]:font-semibold [&_.ProseMirror_h2]:tracking-[-.01em] [&_.ProseMirror_h2]:mt-4 [&_.ProseMirror_h2]:mb-2',
  '[&_.ProseMirror_h3]:text-[14px] [&_.ProseMirror_h3]:font-semibold [&_.ProseMirror_h3]:mt-3 [&_.ProseMirror_h3]:mb-1.5',
  '[&_.ProseMirror_h4]:text-[13px] [&_.ProseMirror_h4]:font-semibold [&_.ProseMirror_h4]:mt-3 [&_.ProseMirror_h4]:mb-1.5',

  // Text blocks
  '[&_.ProseMirror_p]:mb-2',
  '[&_.ProseMirror_a]:text-[var(--color-primary)] [&_.ProseMirror_a]:underline [&_.ProseMirror_a]:underline-offset-2',
  '[&_.ProseMirror_strong]:font-semibold',
  '[&_.ProseMirror_code]:font-[var(--font-mono)] [&_.ProseMirror_code]:text-[0.875em] [&_.ProseMirror_code]:rounded [&_.ProseMirror_code]:bg-[var(--color-input)] [&_.ProseMirror_code]:px-1 [&_.ProseMirror_code]:py-0.5',
  // The code block owns its own <pre>, so the inline-code chrome must not
  // apply inside it.
  '[&_.ProseMirror_pre_code]:bg-transparent [&_.ProseMirror_pre_code]:p-0',
  '[&_.ProseMirror_blockquote]:border-l-2 [&_.ProseMirror_blockquote]:border-[var(--module-codex)] [&_.ProseMirror_blockquote]:pl-3 [&_.ProseMirror_blockquote]:text-[var(--color-text-muted)] [&_.ProseMirror_blockquote]:my-3',
  '[&_.ProseMirror_hr]:border-[var(--color-border)] [&_.ProseMirror_hr]:my-4',

  // Lists
  '[&_.ProseMirror_ul]:list-disc [&_.ProseMirror_ul]:pl-6 [&_.ProseMirror_ul]:mb-2',
  '[&_.ProseMirror_ol]:list-decimal [&_.ProseMirror_ol]:pl-6 [&_.ProseMirror_ol]:mb-2',
  '[&_.ProseMirror_li]:mb-0.5',
  '[&_.ProseMirror_li>p]:mb-0',
  // Task lists carry their own checkbox, so they must not also carry a marker.
  '[&_.ProseMirror_ul[data-type="taskList"]]:list-none [&_.ProseMirror_ul[data-type="taskList"]]:pl-1',
  '[&_.ProseMirror_li[data-type="taskItem"]]:flex [&_.ProseMirror_li[data-type="taskItem"]]:items-start [&_.ProseMirror_li[data-type="taskItem"]]:gap-2',
  '[&_.ProseMirror_li[data-type="taskItem"]>label]:mt-1',

  // Tables
  '[&_.ProseMirror_table]:w-full [&_.ProseMirror_table]:my-3 [&_.ProseMirror_table]:border-collapse [&_.ProseMirror_table]:table-fixed',
  '[&_.ProseMirror_th]:border [&_.ProseMirror_th]:border-[var(--color-border)] [&_.ProseMirror_th]:bg-[var(--color-surface-hover)] [&_.ProseMirror_th]:px-2 [&_.ProseMirror_th]:py-1 [&_.ProseMirror_th]:text-left [&_.ProseMirror_th]:text-[var(--color-text-muted)] [&_.ProseMirror_th]:font-semibold',
  '[&_.ProseMirror_td]:border [&_.ProseMirror_td]:border-[var(--color-border)] [&_.ProseMirror_td]:px-2 [&_.ProseMirror_td]:py-1 [&_.ProseMirror_td]:align-top',
  '[&_.ProseMirror_.selectedCell]:bg-[color-mix(in_srgb,var(--module-codex)_18%,transparent)]',

  // An unresolved wikilink: a page named but not yet written.
  //
  // Dashed and dimmed rather than the ordinary link colour, because it is not
  // an ordinary link — following it does not go anywhere, it offers to create
  // the page. Visibly different is the whole point: an author skimming their
  // own draft should be able to see at a glance which references are still
  // promises.
  //
  // Keyed on `data-unresolved`, which the link mark renders from its
  // `target_title` attribute. A class would be lost the moment ProseMirror
  // re-rendered the mark from its attributes.
  '[&_.ProseMirror_a[data-unresolved]]:text-[var(--color-text-muted)]',
  '[&_.ProseMirror_a[data-unresolved]]:decoration-dashed [&_.ProseMirror_a[data-unresolved]]:underline-offset-4',
  '[&_.ProseMirror_a[data-unresolved]]:cursor-pointer',

  // Preserved formatting: the mark has no node view, so this dotted underline
  // is the only thing that tells a reader the styling was kept rather than
  // rendered (ADR-0012 — visibly preserved, never silently approximated).
  '[&_.codex-unknown-mark]:underline [&_.codex-unknown-mark]:decoration-dotted [&_.codex-unknown-mark]:decoration-[var(--module-codex)] [&_.codex-unknown-mark]:underline-offset-2',

  // The editor's own selection chrome.
  '[&_.ProseMirror-selectednode]:outline-none',
  '[&_.ProseMirror_.ProseMirror-gapcursor]:after:border-t-[var(--color-text)]',
].join(' ');
