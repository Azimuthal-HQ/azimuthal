import { render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';

import {
  MARK_UNKNOWN_MARK,
  NODE_UNKNOWN_CONTENT,
  NODE_UNKNOWN_INLINE,
  PRESERVED_ATTRS,
} from '../../../lib/codex/schema';
import type { CodexDoc } from '../../../lib/codex/schema';
import { CodexDocRenderer } from '../CodexDocRenderer';

/**
 * The reading surface, and ADR-0012 section 2 in particular:
 *
 * > It renders as a visible, labelled placeholder — not an error, not a blank
 * > space. A reader can see that something exists here, what it was, and that
 * > it has been preserved.
 *
 * The failure this guards against is not a crash. It is a page that looks
 * complete and is not — a reader seeing nothing where a diagram used to be,
 * and having no way to know. So the assertions are about what is *shown*: the
 * original type, its source, and its text.
 */

function preserved(type: string, id: string, name: string, text: string) {
  return {
    type,
    attrs: {
      [PRESERVED_ATTRS.id]: id,
      [PRESERVED_ATTRS.name]: name,
      [PRESERVED_ATTRS.source]: 'document',
      [PRESERVED_ATTRS.raw]: '{"type":"' + name + '"}',
      [PRESERVED_ATTRS.text]: text,
    },
  };
}

const DOC: CodexDoc = {
  type: 'doc',
  content: [
    { type: 'heading', attrs: { level: 2 }, content: [{ type: 'text', text: 'Escalation' }] },
    preserved(NODE_UNKNOWN_CONTENT, 'u1', 'ac:structured-macro', 'Gliffy: network topology'),
    {
      type: 'paragraph',
      content: [
        { type: 'text', text: 'Ping ' },
        preserved(NODE_UNKNOWN_INLINE, 'u2', 'ac:emoticon', '(warning)'),
        { type: 'text', text: ' the on-call.' },
        {
          type: 'text',
          text: ' highlighted',
          marks: [
            {
              type: MARK_UNKNOWN_MARK,
              attrs: {
                [PRESERVED_ATTRS.id]: 'u3',
                [PRESERVED_ATTRS.name]: 'textColor',
                [PRESERVED_ATTRS.source]: 'document',
                [PRESERVED_ATTRS.raw]: '{"type":"textColor"}',
                [PRESERVED_ATTRS.text]: '',
              },
            },
          ],
        },
      ],
    },
    {
      type: 'panel',
      attrs: { kind: 'warning' },
      content: [{ type: 'paragraph', content: [{ type: 'text', text: 'Do not restart in peak.' }] }],
    },
  ],
};

function renderDoc(doc: CodexDoc = DOC) {
  return render(
    <MemoryRouter>
      <CodexDocRenderer doc={doc} spaceId="space-1" pageId="page-1" pages={[]} />
    </MemoryRouter>,
  );
}

describe('the reading surface renders preserved content visibly', () => {
  it('labels a preserved block with its original type and its source', async () => {
    renderDoc();
    const block = await screen.findByTestId('codex-preserved-block');
    expect(within(block).getByText('Preserved')).toBeInTheDocument();
    expect(within(block).getByText('ac:structured-macro')).toBeInTheDocument();
    // "from this page" — az_source is `document`, meaning it was already
    // inside a stored Codex document rather than brought in by an importer.
    expect(block).toHaveTextContent(/from this page/i);
  });

  it('shows the preserved text rather than an empty box', async () => {
    renderDoc();
    const block = await screen.findByTestId('codex-preserved-block');
    expect(block).toHaveTextContent('Gliffy: network topology');
    expect(block).toHaveTextContent(/cannot display this content/i);
  });

  it('renders the inline placeholder in the sentence, not as a gap', async () => {
    renderDoc();
    const inline = await screen.findByTestId('codex-preserved-inline');
    expect(inline).toHaveTextContent('(warning)');
    // The sentence still reads.
    expect(await screen.findByTestId('codex-document')).toHaveTextContent(
      /Ping .*\(warning\).* the on-call\./,
    );
  });

  it('keeps preserved formatting marked rather than silently approximating it', async () => {
    renderDoc();
    await screen.findByTestId('codex-document');
    const marked = document.querySelector('.codex-unknown-mark');
    expect(marked).not.toBeNull();
    expect(marked).toHaveTextContent('highlighted');
    expect(marked?.getAttribute('title')).toMatch(/preserved formatting: textColor/i);
  });

  it('renders a preserved block as inert, not as an editable region', async () => {
    // The document as a whole is not editable here, and the placeholder's own
    // body is explicitly not: a typed-into placeholder would show a body that
    // no longer matches the bytes the server holds.
    renderDoc();
    const block = await screen.findByTestId('codex-preserved-block');
    expect(block.querySelector('[contenteditable="true"]')).toBeNull();
    await waitFor(() => {
      expect(document.querySelector('.ProseMirror')).toHaveAttribute('contenteditable', 'false');
    });
  });

  it('renders macros natively — the same components the editor uses', async () => {
    renderDoc();
    const panel = await screen.findByTestId('codex-panel');
    expect(panel).toHaveAttribute('data-kind', 'warning');
    expect(panel).toHaveTextContent('Do not restart in peak.');
    // A reader is shown the kind, not offered the editor's picker.
    expect(within(panel).queryByTestId('codex-panel-kind')).not.toBeInTheDocument();
    expect(within(panel).getByText('Warning')).toBeInTheDocument();
  });

  it('renders inside an <article>', async () => {
    // The wiki E2E scopes its persistence assertions to <article> so that a
    // contentEditable full of just-typed text cannot satisfy a check meant to
    // prove the page was saved and re-rendered. Keeping the element is part
    // of that contract, not incidental markup.
    const { container } = renderDoc();
    await screen.findByTestId('codex-document');
    expect(container.querySelector('article')).not.toBeNull();
  });
});
