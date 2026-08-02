import ReactMarkdown from 'react-markdown';
import { cn } from '../lib/utils';

interface MarkdownProps {
  /** The markdown source. Empty renders the fallback, or nothing. */
  children: string;
  /** Rendered in place of an empty body. */
  fallback?: React.ReactNode;
  className?: string;
  testId?: string;
}

/**
 * The shared read-only markdown renderer.
 *
 * It exists because four pages had each hand-rolled the same `<ReactMarkdown>`
 * plus the same six-line prose class list, and a fifth copy was about to ship
 * with the P5 note gadget. docs/design/shared-surfaces.md calls a second
 * implementation of something like this a defect rather than a convenience.
 *
 * RAW HTML IS NOT ENABLED, and that is the point. react-markdown v10 escapes
 * embedded HTML by default; turning it back on means `rehype-raw`. A note
 * gadget's body is user-authored text that lands on somebody else's dashboard
 * when the dashboard is shared, so it renders as markdown and never as markup.
 * `pages/codex/WikiPage.tsx` deliberately keeps its own call site because it
 * DOES pass rehype-raw for legacy wiki content; that is a Codex decision.
 *
 * That paragraph used to continue "and there is no sanitiser behind it
 * anywhere in this codebase". True when written; false since the v0.4.1 trust
 * patch, which put `rehype-sanitize` immediately after `rehype-raw` at that one
 * call site. Corrected rather than deleted, because the conclusion for THIS
 * component is unchanged: enabling raw HTML here would mean owning a sanitiser
 * schema for a surface with no need of markup at all.
 *
 * EVERY PROSE COLOUR IS PINNED TO A TOKEN. The app's theme is the `.dark`
 * class while `prose-invert` keys off the OS media query, so the two desync —
 * a body styled with `dark:prose-invert` alone renders light-on-light for
 * anybody whose system theme disagrees with their app theme.
 */
export function Markdown({ children, fallback, className, testId }: MarkdownProps) {
  const body = children?.trim() ?? '';
  if (!body) {
    return fallback ? <>{fallback}</> : null;
  }
  return (
    <div
      data-testid={testId}
      className={cn(
        'prose prose-sm max-w-none leading-[1.7]',
        'prose-headings:text-[var(--color-text)] prose-headings:font-semibold',
        'prose-p:text-[var(--color-text)] prose-li:text-[var(--color-text)] prose-strong:text-[var(--color-text)]',
        'prose-a:text-[var(--color-primary)]',
        'prose-code:font-[var(--font-mono)] prose-code:text-[var(--color-text)] prose-code:bg-[var(--color-input)] prose-code:rounded prose-code:px-1.5 prose-code:py-0.5',
        'prose-pre:bg-[var(--color-input)] prose-pre:border prose-pre:border-[var(--color-border)]',
        className,
      )}
    >
      <ReactMarkdown>{body}</ReactMarkdown>
    </div>
  );
}
