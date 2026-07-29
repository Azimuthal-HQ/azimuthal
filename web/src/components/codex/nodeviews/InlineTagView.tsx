/**
 * The inline `#tag` chip.
 *
 * It is a link to the tag's browse page in the reader and an inert chip in the
 * editor, and the split is deliberate: clicking a tag while writing would
 * navigate away mid-sentence, and a tag you cannot click while reading is not
 * a tag.
 *
 * The label is not editable in place. The node is an atom (see
 * `extensions/tags.ts`), and the label is what the server aggregates into the
 * page's tags at publish — a chip whose visible text could drift from its
 * stored attribute would tag the page with something nobody typed. Changing a
 * tag means deleting the token and typing another.
 */
import { NodeViewWrapper } from '@tiptap/react';
import type { NodeViewProps } from '@tiptap/react';
import { Link } from 'react-router-dom';

import { TAG_ATTRS } from '../../../lib/codex/schema';
import { useCodexDocumentContext } from '../CodexDocumentContext';
import { tagBrowsePath } from '../tagLinks';

const CHIP_CLASSES = [
  'inline-flex items-center rounded-[var(--radius-full)] px-1.5 py-0.5 align-baseline',
  'text-[0.85em] font-medium leading-none',
  'bg-[color-mix(in_srgb,var(--module-codex)_16%,transparent)] text-[var(--module-codex)]',
].join(' ');

export function InlineTagView({ node, editor, selected }: NodeViewProps) {
  const label = String(node.attrs[TAG_ATTRS.label] ?? '');
  const { spaceId } = useCodexDocumentContext();
  const ring = selected ? ' ring-1 ring-[var(--module-codex)]' : '';

  return (
    <NodeViewWrapper as="span" data-testid="codex-inline-tag" data-label={label}>
      <span contentEditable={false} className="inline-block align-baseline">
        {editor.isEditable ? (
          <span className={CHIP_CLASSES + ring}>#{label}</span>
        ) : (
          <Link
            to={tagBrowsePath(spaceId, label)}
            className={CHIP_CLASSES + ' no-underline hover:bg-[color-mix(in_srgb,var(--module-codex)_28%,transparent)]'}
          >
            #{label}
          </Link>
        )}
      </span>
    </NodeViewWrapper>
  );
}
