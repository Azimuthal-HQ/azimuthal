import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type {
  CodexEditableDocument,
  CodexPublishConflict,
  CodexPublishLostContent,
} from '../../../lib/api';
import type { CodexDoc } from '../../../lib/codex/schema';
import { AUTOSAVE_DELAY_MS, PageEditor } from '../PageEditor';

/**
 * The editing session: autosave, and the refusals publish can answer with.
 *
 * The two properties worth the most here are not "a request was sent" but
 * "the right thing was NOT sent":
 *
 * - opening a page must not write a draft, because the reload path loads a
 *   *different* document into the editor and an unguarded autosave would
 *   overwrite the very draft the reload promised to keep;
 * - `base_version` must never move, because the preservation ids in the
 *   document were minted against that version and publish re-derives it to
 *   resolve them. An overwrite that "helpfully" advanced it would splice the
 *   wrong bytes into somebody's page.
 */

const { saveDraftMutate, publishMutate, discardMutate, uploadMutate } = vi.hoisted(() => ({
  saveDraftMutate: vi.fn(),
  publishMutate: vi.fn(),
  discardMutate: vi.fn(),
  uploadMutate: vi.fn(),
}));

// The real error classes and friendlyErrorMessage are kept — PageEditor
// dispatches on `instanceof`, so stubbing them would make the branch under
// test unreachable and the assertions vacuous.
vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useSavePageDraft: () => ({ mutateAsync: saveDraftMutate, isPending: false }),
    usePublishPage: () => ({ mutateAsync: publishMutate, isPending: false }),
    useDiscardPageDraft: () => ({ mutateAsync: discardMutate, isPending: false }),
    useUploadPageImage: () => ({ mutateAsync: uploadMutate, isPending: false }),
  };
});

// The editor surface is exercised by its own tests; here it is a seam that
// lets a change be injected without driving ProseMirror.
// The tags row beside the title is page metadata with its own surface, its own
// queries and its own tests (PageTags.test.tsx). Stubbed here for the same
// reason CodexEditor is: this file is about autosave, publishing and the
// refusals between them, and a real PageTags would put three unmocked queries
// into every one of those tests.
vi.mock('../PageTags', () => ({
  PageTags: () => <div data-testid="codex-page-tags-stub" />,
}));

vi.mock('../CodexEditor', () => ({
  CodexEditor: ({ onChange }: { onChange: (doc: CodexDoc) => void }) => (
    <button
      type="button"
      data-testid="fake-type"
      onClick={() =>
        onChange({ type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'typed' }] }] })
      }
    >
      type something
    </button>
  ),
}));

const PUBLISHED_DOC: CodexDoc = {
  type: 'doc',
  content: [{ type: 'paragraph', content: [{ type: 'text', text: 'published body' }] }],
};

const DRAFT_DOC: CodexDoc = {
  type: 'doc',
  content: [{ type: 'paragraph', content: [{ type: 'text', text: 'draft body' }] }],
};

function documentPayload(overrides: Partial<CodexEditableDocument> = {}): CodexEditableDocument {
  return {
    page_id: 'page-1',
    title: 'Runbook',
    doc: PUBLISHED_DOC,
    base_version: 4,
    source_format: 'document',
    preserved_ids: [],
    draft: null,
    ...overrides,
  };
}

function renderEditor(
  document: CodexEditableDocument,
  extra: { onClose?: () => void; onReloadDocument?: () => Promise<CodexEditableDocument | undefined> } = {},
) {
  return render(
    <PageEditor
      spaceId="space-1"
      pageId="page-1"
      document={document}
      pages={[]}
      onClose={extra.onClose ?? vi.fn()}
      onReloadDocument={extra.onReloadDocument ?? vi.fn(async () => undefined)}
    />,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  publishMutate.mockResolvedValue({ id: 'page-1', version: 5 });
  saveDraftMutate.mockResolvedValue({});
  discardMutate.mockResolvedValue(undefined);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('autosave', () => {
  it('does not save a draft merely because the editor was opened', async () => {
    // The invariant the conflict-reload path depends on. Without it, opening a
    // page — or reloading after a conflict — writes whatever is on screen over
    // the author's stored draft.
    vi.useFakeTimers();
    renderEditor(documentPayload());

    await act(async () => {
      vi.advanceTimersByTime(AUTOSAVE_DELAY_MS * 5);
    });
    expect(saveDraftMutate).not.toHaveBeenCalled();
  });

  it('saves once, after the debounce, however many changes arrive', async () => {
    vi.useFakeTimers();
    renderEditor(documentPayload());

    fireEvent.click(screen.getByTestId('fake-type'));
    fireEvent.click(screen.getByTestId('fake-type'));

    // Still nothing before the delay elapses — otherwise this asserts only
    // that a save happened eventually, which a save-per-keystroke would also
    // satisfy.
    await act(async () => {
      vi.advanceTimersByTime(AUTOSAVE_DELAY_MS - 50);
    });
    expect(saveDraftMutate).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(100);
    });
    expect(saveDraftMutate).toHaveBeenCalledTimes(1);
  });

  it('sends the version the session started from, not the page’s current one', async () => {
    vi.useFakeTimers();
    renderEditor(documentPayload({ base_version: 4 }));

    fireEvent.click(screen.getByTestId('fake-type'));
    await act(async () => {
      vi.advanceTimersByTime(AUTOSAVE_DELAY_MS);
    });

    expect(saveDraftMutate).toHaveBeenCalledWith(
      expect.objectContaining({ base_version: 4, title: 'Runbook' }),
    );
  });

  it('reports a failed autosave without interrupting the author', async () => {
    vi.useFakeTimers();
    saveDraftMutate.mockRejectedValueOnce(new Error('offline'));
    renderEditor(documentPayload());

    fireEvent.click(screen.getByTestId('fake-type'));
    await act(async () => {
      vi.advanceTimersByTime(AUTOSAVE_DELAY_MS);
    });

    expect(screen.getByTestId('codex-save-state')).toHaveAttribute('data-state', 'failed');
    // No dialog: somebody is typing.
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});

describe('opening a page with a draft', () => {
  it('restores the draft, and says so', () => {
    renderEditor(
      documentPayload({
        draft: {
          title: 'Runbook (wip)',
          doc: DRAFT_DOC,
          base_version: 4,
          updated_at: '2026-07-26T10:00:00Z',
          stale: false,
        },
      }),
    );
    expect(screen.getByTestId('codex-draft-restored')).toBeInTheDocument();
    expect(screen.getByTestId('codex-title-input')).toHaveValue('Runbook (wip)');
  });

  it('warns when the page moved on under the draft', () => {
    renderEditor(
      documentPayload({
        base_version: 7,
        draft: {
          title: 'Runbook',
          doc: DRAFT_DOC,
          base_version: 4,
          updated_at: '2026-07-26T10:00:00Z',
          stale: true,
        },
      }),
    );
    expect(screen.getByTestId('codex-draft-restored')).toHaveTextContent(/published again since/i);
  });

  it('publishes a restored draft against the DRAFT’s base version', async () => {
    // The draft's base_version, not the page's. The ids in the draft were
    // minted against version 4; resolving them against 7 would splice the
    // wrong bytes.
    renderEditor(
      documentPayload({
        base_version: 7,
        draft: {
          title: 'Runbook',
          doc: DRAFT_DOC,
          base_version: 4,
          updated_at: '2026-07-26T10:00:00Z',
          stale: true,
        },
      }),
    );
    fireEvent.click(screen.getByTestId('codex-publish'));
    await waitFor(() => expect(publishMutate).toHaveBeenCalled());
    expect(publishMutate).toHaveBeenCalledWith(expect.objectContaining({ base_version: 4 }));
  });
});

describe('publishing', () => {
  it('sends no acknowledgement and no overwrite on the ordinary path', async () => {
    renderEditor(documentPayload());
    fireEvent.click(screen.getByTestId('codex-publish'));

    await waitFor(() => expect(publishMutate).toHaveBeenCalled());
    const sent = publishMutate.mock.calls[0][0];
    // Absent, not false/[]: the server decodes with DisallowUnknownFields and
    // "I acknowledge nothing" has to mean exactly that.
    expect(sent).not.toHaveProperty('acknowledged_lost_ids');
    expect(sent).not.toHaveProperty('overwrite');
  });

  it('closes the editor on success', async () => {
    const onClose = vi.fn();
    renderEditor(documentPayload(), { onClose });
    fireEvent.click(screen.getByTestId('codex-publish'));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});

describe('the lost-content refusal (ADR-0012)', () => {
  const LOST: CodexPublishLostContent = {
    page_id: 'page-1',
    lost_ids: ['u2', 'u5'],
    lost: [
      { id: 'u2', name: 'ac:structured-macro', text: 'Gliffy diagram' },
      { id: 'u5', name: 'legacyBlock', text: 'callout' },
    ],
    message: 'Publishing would remove 2 block(s) of preserved content…',
  };

  it('shows the dialog rather than a toast, naming what would be lost', async () => {
    const { PublishLostContentError } = await import('../../../lib/api');
    publishMutate.mockRejectedValueOnce(new PublishLostContentError(LOST));
    renderEditor(documentPayload());

    fireEvent.click(screen.getByTestId('codex-publish'));
    await screen.findByTestId('codex-lost-content-dialog');
    expect(screen.getByTestId('codex-lost-content-count')).toHaveTextContent('2 preserved items');
    expect(screen.getAllByTestId('codex-lost-content-item')).toHaveLength(2);
  });

  it('republishes with exactly the acknowledged ids once confirmed', async () => {
    const { PublishLostContentError } = await import('../../../lib/api');
    publishMutate.mockRejectedValueOnce(new PublishLostContentError(LOST));
    renderEditor(documentPayload());

    fireEvent.click(screen.getByTestId('codex-publish'));
    await screen.findByTestId('codex-lost-content-dialog');
    fireEvent.click(screen.getByTestId('codex-lost-content-confirm'));

    await waitFor(() => expect(publishMutate).toHaveBeenCalledTimes(2));
    expect(publishMutate.mock.calls[1][0]).toMatchObject({
      acknowledged_lost_ids: ['u2', 'u5'],
      base_version: 4,
    });
  });

  it('publishes nothing at all if the author cancels', async () => {
    const { PublishLostContentError } = await import('../../../lib/api');
    publishMutate.mockRejectedValueOnce(new PublishLostContentError(LOST));
    renderEditor(documentPayload());

    fireEvent.click(screen.getByTestId('codex-publish'));
    await screen.findByTestId('codex-lost-content-dialog');
    fireEvent.click(screen.getByTestId('codex-lost-content-cancel'));

    await waitFor(() =>
      expect(screen.queryByTestId('codex-lost-content-dialog')).not.toBeInTheDocument(),
    );
    expect(publishMutate).toHaveBeenCalledTimes(1);
  });
});

describe('the version conflict', () => {
  const CONFLICT: CodexPublishConflict = {
    page_id: 'page-1',
    expected_version: 4,
    current_page: {
      id: 'page-1',
      space_id: 'space-1',
      title: 'Runbook',
      content: '',
      doc: null,
      version: 9,
      parent_id: null,
      author_id: 'u2',
      path: 'runbook',
      created_at: '',
      updated_at: '',
    },
    message: 'This page was published again while you were editing…',
  };

  it('overwrites at the ORIGINAL base version', async () => {
    // #73: "An overwrite resolves the draft's preserved content against the
    // revision at base_version, not against the current page."
    const { PublishConflictError } = await import('../../../lib/api');
    publishMutate.mockRejectedValueOnce(new PublishConflictError(CONFLICT));
    renderEditor(documentPayload({ base_version: 4 }));

    fireEvent.click(screen.getByTestId('codex-publish'));
    await screen.findByTestId('codex-conflict-dialog');
    fireEvent.click(screen.getByTestId('codex-conflict-overwrite'));

    await waitFor(() => expect(publishMutate).toHaveBeenCalledTimes(2));
    expect(publishMutate.mock.calls[1][0]).toMatchObject({ overwrite: true, base_version: 4 });
  });

  it('carries the overwrite decision through a lost-content refusal', async () => {
    // Otherwise the republish hits the version guard again and the two dialogs
    // bounce off each other forever.
    const { PublishConflictError, PublishLostContentError } = await import('../../../lib/api');
    publishMutate
      .mockRejectedValueOnce(new PublishConflictError(CONFLICT))
      .mockRejectedValueOnce(
        new PublishLostContentError({
          page_id: 'page-1',
          lost_ids: ['u2'],
          lost: [{ id: 'u2', name: 'ac:structured-macro', text: '' }],
          message: '…',
        }),
      );
    renderEditor(documentPayload({ base_version: 4 }));

    fireEvent.click(screen.getByTestId('codex-publish'));
    await screen.findByTestId('codex-conflict-dialog');
    fireEvent.click(screen.getByTestId('codex-conflict-overwrite'));

    await screen.findByTestId('codex-lost-content-dialog');
    // One dialog at a time — two stacked modals is not a dialogue.
    expect(screen.queryByTestId('codex-conflict-dialog')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('codex-lost-content-confirm'));
    await waitFor(() => expect(publishMutate).toHaveBeenCalledTimes(3));
    expect(publishMutate.mock.calls[2][0]).toMatchObject({
      overwrite: true,
      acknowledged_lost_ids: ['u2'],
      base_version: 4,
    });
  });

  it('reload fetches the newer document and abandons the overwrite decision', async () => {
    const { PublishConflictError } = await import('../../../lib/api');
    publishMutate.mockRejectedValueOnce(new PublishConflictError(CONFLICT));
    const reloaded = documentPayload({ base_version: 9, doc: PUBLISHED_DOC, title: 'Runbook v9' });
    const onReloadDocument = vi.fn(async () => reloaded);

    renderEditor(documentPayload({ base_version: 4 }), { onReloadDocument });

    fireEvent.click(screen.getByTestId('codex-publish'));
    await screen.findByTestId('codex-conflict-dialog');
    fireEvent.click(screen.getByTestId('codex-conflict-reload'));

    await waitFor(() => expect(onReloadDocument).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByTestId('codex-title-input')).toHaveValue('Runbook v9'));

    // Publishing now starts from the new base, with no overwrite carried over
    // — the author is looking at the newer version and has agreed to nothing.
    fireEvent.click(screen.getByTestId('codex-publish'));
    await waitFor(() => expect(publishMutate).toHaveBeenCalledTimes(2));
    const second = publishMutate.mock.calls[1][0];
    expect(second).toMatchObject({ base_version: 9 });
    expect(second).not.toHaveProperty('overwrite');
  });

  it('does not overwrite the stored draft when reloading', async () => {
    // "Reload — your draft is kept" is only true if the reloaded published
    // document is not immediately autosaved over the draft. Real timers for
    // the interaction (findBy* waits on them), fake ones only for the window
    // in which an unguarded autosave would fire.
    const { PublishConflictError } = await import('../../../lib/api');
    publishMutate.mockRejectedValueOnce(new PublishConflictError(CONFLICT));
    const onReloadDocument = vi.fn(async () => documentPayload({ base_version: 9 }));

    renderEditor(
      documentPayload({
        base_version: 4,
        draft: {
          title: 'Runbook',
          doc: DRAFT_DOC,
          base_version: 4,
          updated_at: '',
          stale: false,
        },
      }),
      { onReloadDocument },
    );

    fireEvent.click(screen.getByTestId('codex-publish'));
    fireEvent.click(await screen.findByTestId('codex-conflict-reload'));
    await waitFor(() => expect(onReloadDocument).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.queryByTestId('codex-conflict-dialog')).not.toBeInTheDocument(),
    );

    saveDraftMutate.mockClear();
    vi.useFakeTimers();
    await act(async () => {
      vi.advanceTimersByTime(AUTOSAVE_DELAY_MS * 3);
    });
    expect(saveDraftMutate).not.toHaveBeenCalled();
  });
});

describe('discarding a draft', () => {
  it('asks first, and does nothing until confirmed', async () => {
    renderEditor(documentPayload());
    fireEvent.click(screen.getByTestId('codex-discard-draft'));

    await screen.findByTestId('codex-discard-dialog');
    expect(discardMutate).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('codex-discard-confirm'));
    await waitFor(() => expect(discardMutate).toHaveBeenCalledTimes(1));
  });
});
