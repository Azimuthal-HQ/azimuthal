/**
 * The code block, with its language picker.
 *
 * `language` is the attribute the markdown projection reads for the fence, so
 * the picker is not cosmetic: it decides what a reader sees and what search
 * indexes. A native `<select>` rather than a custom dropdown — it is keyboard-
 * and screen-reader-operable without any of the work, which matters here
 * because it sits inside a contentEditable region where a bespoke listbox has
 * to fight ProseMirror for focus.
 */
import { NodeViewContent, NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';

import { CODE_LANGUAGES } from '../lowlight';

export function CodeBlockView({ node, updateAttributes, editor }: NodeViewProps) {
  const language = String(node.attrs.language ?? '');

  return (
    <NodeViewWrapper className="my-3" data-testid="codex-code-block">
      <div className="overflow-hidden rounded-[var(--radius-lg)] border border-[var(--color-border)]">
        <div
          className="flex items-center justify-between border-b border-[var(--color-border)] bg-[var(--color-surface-hover)] px-2 py-1"
          contentEditable={false}
        >
          {editor.isEditable ? (
            <label className="flex items-center gap-1.5">
              <span className="sr-only">Code language</span>
              <select
                value={language}
                onChange={(e) => updateAttributes({ language: e.target.value })}
                data-testid="codex-code-language"
                className="rounded-[var(--radius-sm)] border border-[var(--color-border)] bg-[var(--color-input)] px-1.5 py-0.5 text-[var(--text-xs)] text-[var(--color-text)] focus:border-[var(--module-codex)] focus:outline-none"
              >
                {CODE_LANGUAGES.map((l) => (
                  <option key={l.value} value={l.value}>
                    {l.label}
                  </option>
                ))}
              </select>
            </label>
          ) : (
            <span className="text-[var(--text-xs)] uppercase tracking-wide text-[var(--color-text-muted)]">
              {CODE_LANGUAGES.find((l) => l.value === language)?.label ?? language ?? 'Plain text'}
            </span>
          )}
        </div>
        <pre className="overflow-x-auto bg-[var(--color-input)] p-3">
          {/* `as` is typed to a fixed element set upstream; a code element is
              the semantically right child of <pre> and the cast is confined
              here rather than loosening the prop's type. */}
          <NodeViewContent
            as={'code' as never}
            className={`font-[var(--font-mono)] text-[var(--text-xs)] leading-relaxed text-[var(--color-text)] ${
              language ? `language-${language} hljs` : ''
            }`}
          />
        </pre>
      </div>
    </NodeViewWrapper>
  );
}
