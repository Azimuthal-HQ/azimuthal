/**
 * The status lozenge: an inline pill with an editable label.
 *
 * The node is an atom, so its label lives in the `text` attribute rather than
 * in child content — which is also what the markdown projection reads. Editing
 * happens in a small popover rather than in place: an atom has no editable
 * interior, and faking one would put ProseMirror's selection somewhere it
 * cannot map.
 */
import { NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { useEffect, useRef, useState } from 'react';

import { LOZENGE_COLORS } from '../extensions/macros';
import type { LozengeColor } from '../extensions/macros';

const LOZENGE_HUES: Record<LozengeColor, string> = {
  neutral: 'var(--color-text-muted)',
  blue: 'var(--color-info)',
  green: 'var(--color-success)',
  yellow: 'var(--color-warning)',
  red: 'var(--color-danger)',
  purple: 'var(--module-codex)',
};

function hueFor(color: string): string {
  return LOZENGE_HUES[color as LozengeColor] ?? LOZENGE_HUES.neutral;
}

export function StatusLozengeView({ node, updateAttributes, editor, selected }: NodeViewProps) {
  const text = String(node.attrs.text ?? '');
  const color = String(node.attrs.color ?? 'neutral');
  const [editing, setEditing] = useState(false);
  const popoverRef = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    if (!editing) return;
    const onDown = (e: MouseEvent) => {
      if (popoverRef.current && !popoverRef.current.contains(e.target as globalThis.Node)) {
        setEditing(false);
      }
    };
    document.addEventListener('mousedown', onDown);
    return () => document.removeEventListener('mousedown', onDown);
  }, [editing]);

  const hue = hueFor(color);

  return (
    <NodeViewWrapper as="span" data-testid="codex-status-lozenge" data-color={color}>
      <span className="relative inline-block align-baseline" contentEditable={false}>
        <button
          type="button"
          disabled={!editor.isEditable}
          onClick={() => setEditing((v) => !v)}
          data-testid="codex-status-lozenge-label"
          className={[
            'rounded-[var(--radius-full)] px-2 py-0.5 text-[var(--text-xs)] font-semibold uppercase tracking-wide',
            'disabled:cursor-text',
            selected ? 'ring-1 ring-[var(--module-codex)]' : '',
          ].join(' ')}
          style={{
            color: hue,
            background: `color-mix(in srgb, ${hue} 18%, transparent)`,
          }}
        >
          {text || 'STATUS'}
        </button>

        {editing && (
          <span
            ref={popoverRef}
            className="absolute left-0 top-full z-50 mt-1 flex w-56 flex-col gap-2 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] p-2 shadow-[var(--shadow-lg)]"
          >
            <input
              // eslint-disable-next-line jsx-a11y/no-autofocus -- the popover is
              // opened by an explicit click; focusing it is what the click asked for.
              autoFocus
              value={text}
              onChange={(e) => updateAttributes({ text: e.target.value })}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === 'Escape') {
                  e.preventDefault();
                  setEditing(false);
                }
              }}
              aria-label="Status label"
              data-testid="codex-status-lozenge-input"
              className="rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-input)] px-2 py-1 text-[var(--text-sm)] text-[var(--color-text)] focus:border-[var(--module-codex)] focus:outline-none"
            />
            <span className="flex flex-wrap gap-1">
              {LOZENGE_COLORS.map((c) => (
                <button
                  key={c}
                  type="button"
                  aria-label={`${c} status`}
                  aria-pressed={c === color}
                  onClick={() => updateAttributes({ color: c })}
                  className={`h-5 w-5 rounded-[var(--radius-full)] border ${
                    c === color ? 'border-[var(--color-text)]' : 'border-[var(--color-border)]'
                  }`}
                  style={{ background: `color-mix(in srgb, ${LOZENGE_HUES[c]} 55%, transparent)` }}
                />
              ))}
            </span>
          </span>
        )}
      </span>
    </NodeViewWrapper>
  );
}
