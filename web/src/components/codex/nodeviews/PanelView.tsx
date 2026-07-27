/**
 * The panel macro: an admonition with an editable body and a kind picker.
 *
 * The picker only appears when the editor is editable, so the reading surface
 * and the editor share this view without the reader being offered controls.
 */
import { NodeViewContent, NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { AlertTriangle, CheckCircle2, Info, StickyNote, XCircle } from 'lucide-react';
import type { ComponentType } from 'react';

import { PANEL_KINDS } from '../extensions/macros';
import type { PanelKind } from '../extensions/macros';

const PANEL_STYLES: Record<
  PanelKind,
  { label: string; hue: string; icon: ComponentType<{ className?: string }> }
> = {
  info: { label: 'Info', hue: 'var(--color-info)', icon: Info },
  note: { label: 'Note', hue: 'var(--module-codex)', icon: StickyNote },
  success: { label: 'Success', hue: 'var(--color-success)', icon: CheckCircle2 },
  warning: { label: 'Warning', hue: 'var(--color-warning)', icon: AlertTriangle },
  error: { label: 'Error', hue: 'var(--color-danger)', icon: XCircle },
};

function styleFor(kind: string): (typeof PANEL_STYLES)[PanelKind] {
  return PANEL_STYLES[kind as PanelKind] ?? PANEL_STYLES.info;
}

export function PanelView({ node, updateAttributes, editor }: NodeViewProps) {
  const kind = String(node.attrs.kind ?? 'info');
  const style = styleFor(kind);
  const Icon = style.icon;

  return (
    <NodeViewWrapper className="my-3" data-testid="codex-panel" data-kind={kind}>
      <div
        className="overflow-hidden rounded-[var(--radius-lg)] border-l-4 border border-[var(--color-border)]"
        style={{
          borderLeftColor: style.hue,
          background: `color-mix(in srgb, ${style.hue} 8%, transparent)`,
        }}
      >
        <div className="flex items-center gap-2 px-3 pt-2" contentEditable={false}>
          <Icon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
          {editor.isEditable ? (
            <label className="flex items-center gap-1.5">
              <span className="sr-only">Panel kind</span>
              <select
                value={kind}
                onChange={(e) => updateAttributes({ kind: e.target.value })}
                data-testid="codex-panel-kind"
                className="rounded-[var(--radius-sm)] border border-[var(--color-border)] bg-[var(--color-input)] px-1.5 py-0.5 text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text)] focus:border-[var(--module-codex)] focus:outline-none"
              >
                {PANEL_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {PANEL_STYLES[k].label}
                  </option>
                ))}
              </select>
            </label>
          ) : (
            <span className="text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text)]">
              {style.label}
            </span>
          )}
        </div>
        <NodeViewContent className="px-3 py-2 [&>*:last-child]:mb-0" />
      </div>
    </NodeViewWrapper>
  );
}
