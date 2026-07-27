import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { CodexPublishConflict, CodexPublishLostContent, WikiPage } from '../../../lib/api';
import {
  LostContentDialog,
  PublishConflictDialog,
  UnresolvablePreservedDialog,
} from '../PublishDialogs';

/**
 * The publish refusals, as a person meets them.
 *
 * PR #73 made the *order* of these refusals the server's contract; this is the
 * other half — that each one reaches the author as something they can act on
 * rather than as an error toast. The lost-content dialog in particular is the
 * only place ADR-0012's catastrophe can be caught in the act, so what it says
 * and what it sends are both asserted.
 */

const CURRENT_PAGE = {
  id: 'page-1',
  space_id: 'space-1',
  title: 'Runbook',
  content: '',
  doc: null,
  version: 7,
  parent_id: null,
  author_id: 'u1',
  path: 'runbook',
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-26T00:00:00Z',
} satisfies WikiPage;

const CONFLICT: CodexPublishConflict = {
  page_id: 'page-1',
  expected_version: 4,
  current_page: CURRENT_PAGE,
  message:
    'This page was published again while you were editing — it is now at version 7, and your draft started from version 4. Reload to see the new version (your draft is kept), or publish anyway to replace it.',
};

const LOST: CodexPublishLostContent = {
  page_id: 'page-1',
  lost_ids: ['u1', 'u3'],
  lost: [
    { id: 'u1', name: 'ac:structured-macro', text: 'Gliffy diagram: network topology' },
    { id: 'u3', name: 'legacyBlock', text: '<div class="callout">Escalation path</div>' },
  ],
  message:
    'Publishing would remove 2 block(s) of preserved content that this editor cannot display. That content is still in the published page. Reload the page to get it back, or confirm the removal if you meant to delete it.',
};

describe('PublishConflictDialog', () => {
  it('shows the server’s account of the conflict verbatim, and both version numbers', () => {
    render(
      <PublishConflictDialog
        detail={CONFLICT}
        onReload={vi.fn()}
        onOverwrite={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    // The 409 body carries no error code, so friendlyErrorMessage would
    // swallow it; showing it is a deliberate, documented exception because
    // this prose IS the dialogue (document_handler.go:218).
    expect(screen.getByTestId('codex-conflict-message')).toHaveTextContent(
      'it is now at version 7',
    );
    expect(screen.getByTestId('codex-conflict-dialog')).toHaveTextContent('4');
    expect(screen.getByTestId('codex-conflict-dialog')).toHaveTextContent('7');
  });

  it('offers reload and overwrite as separate, differently-worded choices', () => {
    const onReload = vi.fn();
    const onOverwrite = vi.fn();
    render(
      <PublishConflictDialog
        detail={CONFLICT}
        onReload={onReload}
        onOverwrite={onOverwrite}
        onCancel={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByTestId('codex-conflict-reload'));
    expect(onReload).toHaveBeenCalledTimes(1);
    expect(onOverwrite).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('codex-conflict-overwrite'));
    expect(onOverwrite).toHaveBeenCalledTimes(1);
  });

  it('says what overwriting does, in those words', () => {
    // "Publish anyway" hides that this discards somebody else's published
    // work. The button has to name the consequence.
    render(
      <PublishConflictDialog
        detail={CONFLICT}
        onReload={vi.fn()}
        onOverwrite={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByTestId('codex-conflict-overwrite')).toHaveTextContent(/replace/i);
    expect(screen.getByTestId('codex-conflict-dialog')).toHaveTextContent(/your draft/i);
  });
});

describe('LostContentDialog', () => {
  it('states the count and names every item that would be removed', () => {
    render(<LostContentDialog detail={LOST} onConfirm={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId('codex-lost-content-count')).toHaveTextContent(
      'permanently remove 2 preserved items',
    );
    const items = screen.getAllByTestId('codex-lost-content-item');
    expect(items).toHaveLength(2);
    // Named, not enumerated as opaque ids — the whole point of the server
    // sending `lost` alongside `lost_ids`.
    expect(items[0]).toHaveTextContent('ac:structured-macro');
    expect(items[0]).toHaveTextContent('Gliffy diagram: network topology');
    // The converter's internal catch-all name is translated for a reader.
    expect(items[1]).toHaveTextContent('Original markdown block');
  });

  it('uses the count the SERVER sent, not a count of anything local', () => {
    // shared-surfaces.md section 6: a count shown before a destructive
    // confirmation comes from the API. Here that is structural — the client
    // never computed it, and this asserts the singular/plural comes from the
    // payload too.
    const one: CodexPublishLostContent = {
      ...LOST,
      lost_ids: ['u1'],
      lost: [LOST.lost[0]],
    };
    render(<LostContentDialog detail={one} onConfirm={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getByTestId('codex-lost-content-count')).toHaveTextContent(
      'permanently remove 1 preserved item:',
    );
    expect(screen.getByTestId('codex-lost-content-confirm')).toHaveTextContent('Remove 1 item');
  });

  it('requires the confirming click — cancelling reports nothing', () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    render(<LostContentDialog detail={LOST} onConfirm={onConfirm} onCancel={onCancel} />);

    fireEvent.click(screen.getByTestId('codex-lost-content-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('codex-lost-content-confirm'));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('still lists something when the server sent ids but no descriptions', () => {
    // `lost` entries are filled from the captured map; an id with no capture
    // behind it yields a bare entry. Showing nothing at all would be a
    // confirmation dialog with an empty list.
    const bare: CodexPublishLostContent = { ...LOST, lost: [] };
    render(<LostContentDialog detail={bare} onConfirm={vi.fn()} onCancel={vi.fn()} />);
    expect(screen.getAllByTestId('codex-lost-content-item')).toHaveLength(2);
  });
});

describe('UnresolvablePreservedDialog', () => {
  it('offers reload and no way to publish anyway', () => {
    // A 422 means the request does not add up, not that the author has a
    // choice to make. Offering "publish anyway" would send them round a loop.
    render(
      <UnresolvablePreservedDialog
        message="This edit could not be matched to the version of the page it started from."
        onReload={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(screen.getByTestId('codex-unresolvable-reload')).toBeInTheDocument();
    expect(screen.queryByText(/publish anyway/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/replace/i)).not.toBeInTheDocument();
    expect(screen.getByTestId('codex-unresolvable-dialog')).toHaveTextContent(
      /nothing has been published/i,
    );
  });
});
