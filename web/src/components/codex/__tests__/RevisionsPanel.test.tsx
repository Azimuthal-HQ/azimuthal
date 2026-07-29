import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type {
  CodexPublishLostContent,
  WikiDiffSegment,
  WikiRevision,
  WikiRevisionDiff,
} from '../../../lib/api';
import { RevisionsPanel } from '../RevisionsPanel';

/**
 * The history panel: who wrote each version, what changed between two of them,
 * and putting one back.
 *
 * Three of these assertions are the ones worth having:
 *
 * - "Unknown" for a removed account. The alternative failures are both silent —
 *   a row that disappears from a page's history, or an author name invented for
 *   an old revision — and neither would show up as an error anywhere.
 * - the acknowledged ids in the *second* restore call. ADR-0012's confirmation
 *   is only worth anything if what the reader confirmed is what gets sent, so
 *   the argument is asserted rather than the dialog's disappearance.
 * - `restored: false` reaching the reader as a message and not as an error. It
 *   is a 200 with a sentence in it; treating it as a failure would tell someone
 *   that something went wrong when nothing did.
 */

const { restoreMutate, diffRequests, refetchSpy } = vi.hoisted(() => ({
  restoreMutate: vi.fn(),
  diffRequests: vi.fn(),
  refetchSpy: vi.fn(),
}));

/** Mutable fixtures the mocked hooks read, so a test can vary them. */
const state = vi.hoisted(() => ({
  revisions: [] as unknown[],
  diff: null as unknown,
}));

// The real error classes and friendlyErrorMessage are kept: the panel
// dispatches on `instanceof`, so a stubbed module would make the refusal
// branches unreachable and every assertion about them vacuous.
vi.mock('../../../lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../../lib/api')>();
  return {
    ...actual,
    useWikiRevisions: () => ({
      data: state.revisions,
      isLoading: false,
      refetch: refetchSpy,
    }),
    useRestoreWikiRevision: () => ({ mutateAsync: restoreMutate, isPending: false }),
    // Mirrors the real hook's `enabled`: two different, real versions or no
    // data at all. Without that, a test could "render a diff" while the panel
    // had asked for a comparison the server would never have been sent.
    useWikiDiff: (_spaceId: string, _pageId: string, from: number, to: number) => {
      diffRequests(from, to);
      const enabled = from > 0 && to > 0 && from !== to;
      return { data: enabled ? state.diff : undefined, isLoading: false, error: null };
    },
  };
});

function revision(version: number, authorName: string | null): WikiRevision {
  return {
    id: `rev-${version}`,
    page_id: 'page-1',
    version,
    title: 'Runbook',
    author_id: `u${version}`,
    author_name: authorName,
    created_at: `2026-07-0${version}T09:30:00Z`,
  };
}

const DIFF: WikiRevisionDiff = {
  from_version: 1,
  to_version: 3,
  title_segments: null,
  content_segments: [
    { op: 0, text: 'Restart the ' },
    { op: -1, text: 'primary' },
    { op: 1, text: 'standby' },
    { op: 0, text: ' node.' },
  ],
};

const LOST: CodexPublishLostContent = {
  page_id: 'page-1',
  lost_ids: ['u2', 'u5'],
  lost: [
    { id: 'u2', name: 'ac:structured-macro', text: 'Gliffy diagram' },
    { id: 'u5', name: 'legacyBlock', text: 'callout' },
  ],
  message: 'Restoring would remove 2 block(s) of preserved content…',
};

function renderPanel(currentVersion = 5) {
  return render(
    <RevisionsPanel
      spaceId="space-1"
      pageId="page-1"
      currentVersion={currentVersion}
      onClose={vi.fn()}
    />,
  );
}

/** The rows, newest first — the order the endpoint returns them in. */
function rows() {
  return screen.getAllByTestId('codex-revision-select');
}

beforeEach(() => {
  vi.clearAllMocks();
  state.revisions = [revision(5, 'Ada Lovelace'), revision(3, null), revision(2, 'Grace Hopper')];
  state.diff = DIFF;
  restoreMutate.mockResolvedValue({ restored: true, page: { id: 'page-1', version: 6 } });
});

describe('who wrote each version', () => {
  it('names the author on every row', () => {
    renderPanel();
    const authors = screen.getAllByTestId('codex-revision-author').map((el) => el.textContent);
    expect(authors).toEqual(['Ada Lovelace', 'Unknown', 'Grace Hopper']);
  });

  it('says "Unknown" for a removed account rather than dropping the revision', () => {
    // The row survives its author. A history that quietly loses versions when
    // somebody leaves the organisation is the failure this guards against.
    renderPanel();
    expect(screen.getAllByTestId('codex-revision-row')).toHaveLength(3);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });
});

describe('comparing two versions', () => {
  it('asks for nothing until a second version is picked', () => {
    renderPanel();
    fireEvent.click(rows()[2]); // v2

    // Every call the panel made was for a comparison the hook would refuse to
    // run — which is the same as having asked for nothing.
    expect(
      diffRequests.mock.calls.every(([from, to]) => from === 0 || to === 0 || from === to),
    ).toBe(true);
    expect(screen.queryByTestId('codex-revision-diff')).not.toBeInTheDocument();
  });

  it('fetches the picked range in version order and renders the runs', () => {
    renderPanel();
    fireEvent.click(rows()[1]); // v3, the newer of the two, clicked first
    fireEvent.click(rows()[2]); // v2

    // Sorted, not click-ordered: from must be the older version.
    expect(diffRequests).toHaveBeenLastCalledWith(2, 3);
    expect(screen.getByTestId('codex-revision-diff')).toBeInTheDocument();
  });

  it('distinguishes removed from added text in the markup, not only by colour', () => {
    renderPanel();
    fireEvent.click(rows()[1]);
    fireEvent.click(rows()[2]);

    const removed = screen.getByTestId('codex-diff-removed');
    const added = screen.getByTestId('codex-diff-added');
    expect(removed).toHaveTextContent('primary');
    expect(added).toHaveTextContent('standby');
    // <del>/<ins> carry the meaning for a screen reader, a printout, or a
    // reader who cannot tell the two hues apart. Asserting the tags is what
    // makes this fail if the runs ever collapse into identically styled spans.
    expect(removed.tagName).toBe('DEL');
    expect(added.tagName).toBe('INS');
    expect(screen.getAllByTestId('codex-diff-unchanged').map((el) => el.textContent)).toEqual([
      'Restart the ',
      ' node.',
    ]);
  });

  it('says it is a text comparison and that it does not compare structure', () => {
    renderPanel();
    fireEvent.click(rows()[1]);
    fireEvent.click(rows()[2]);

    const diff = screen.getByTestId('codex-revision-diff');
    expect(diff).toHaveTextContent(/text comparison/i);
    expect(diff).toHaveTextContent(/not their structure/i);
  });

  it.each([
    ['null', null],
    ['empty', [] as WikiDiffSegment[]],
  ])('omits the title row when title_segments is %s', (_label, titleSegments) => {
    // Both spellings mean "the title did not change" — the field is documented
    // as empty in that case and arrives as null over the wire — so a title row
    // for either would report a change that never happened.
    state.diff = { ...DIFF, title_segments: titleSegments } satisfies WikiRevisionDiff;
    renderPanel();
    fireEvent.click(rows()[1]);
    fireEvent.click(rows()[2]);
    expect(screen.getByTestId('codex-revision-diff')).toBeInTheDocument();
    expect(screen.queryByTestId('codex-diff-title')).not.toBeInTheDocument();
  });

  it('shows the title row when the title did change', () => {
    state.diff = {
      ...DIFF,
      title_segments: [{ op: -1, text: 'Runbook' }, { op: 1, text: 'Runbook (2026)' }],
    } satisfies WikiRevisionDiff;
    renderPanel();
    fireEvent.click(rows()[1]);
    fireEvent.click(rows()[2]);
    expect(screen.getByTestId('codex-diff-title')).toHaveTextContent('Runbook (2026)');
  });

  it('starts a new selection on the third click', () => {
    renderPanel();
    fireEvent.click(rows()[1]);
    fireEvent.click(rows()[2]);
    expect(screen.getByTestId('codex-revision-diff')).toBeInTheDocument();

    fireEvent.click(rows()[0]); // v5 — a third click, so the range is dropped
    expect(screen.queryByTestId('codex-revision-diff')).not.toBeInTheDocument();
    expect(rows()[0]).toHaveAttribute('aria-pressed', 'true');
    expect(rows()[1]).toHaveAttribute('aria-pressed', 'false');
  });
});

describe('restoring', () => {
  it('offers no restore on the version the page already holds', () => {
    renderPanel(5);
    // The other rows do offer one, so this is not passing because the button
    // is missing everywhere.
    expect(screen.getByRole('button', { name: 'Restore version 3' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Restore version 5' })).not.toBeInTheDocument();
  });

  it('asks first, and says the restore appends rather than rewrites', async () => {
    renderPanel(5);
    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));

    const dialog = await screen.findByTestId('codex-restore-confirm-dialog');
    expect(dialog).toHaveTextContent(/new/i);
    expect(dialog).toHaveTextContent(/history/i);
    // Nothing is sent while the question is still open.
    expect(restoreMutate).not.toHaveBeenCalled();
  });

  it('restores against the current version as the guard', async () => {
    renderPanel(5);
    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));
    fireEvent.click(await screen.findByTestId('codex-restore-confirm'));

    await waitFor(() => expect(restoreMutate).toHaveBeenCalled());
    const sent = restoreMutate.mock.calls[0][0];
    expect(sent).toMatchObject({ version: 2, base_version: 5 });
    // Absent, not empty: the server decodes with DisallowUnknownFields, and
    // "I acknowledge nothing" has to mean exactly that.
    expect(sent).not.toHaveProperty('acknowledged_lost_ids');
    expect(sent).not.toHaveProperty('overwrite');
  });

  it('reports a no-op restore as the success it is, with the server’s sentence', async () => {
    restoreMutate.mockResolvedValueOnce({
      restored: false,
      message: "Version 2 is already this page's content, so there was nothing to restore.",
    });
    renderPanel(5);
    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));
    fireEvent.click(await screen.findByTestId('codex-restore-confirm'));

    const notice = await screen.findByTestId('codex-restore-notice');
    expect(notice).toHaveTextContent('there was nothing to restore');
    // The half that matters: no error surface anywhere. Nothing went wrong.
    expect(screen.queryByTestId('codex-restore-error')).not.toBeInTheDocument();
  });

  it('sends any other failure through friendlyErrorMessage', async () => {
    restoreMutate.mockRejectedValueOnce(new Error('socket hang up'));
    renderPanel(5);
    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));
    fireEvent.click(await screen.findByTestId('codex-restore-confirm'));

    const error = await screen.findByTestId('codex-restore-error');
    expect(error).toHaveTextContent('That version was not restored.');
    // The raw error text is not what a reader is shown.
    expect(error).not.toHaveTextContent('socket hang up');
  });
});

describe('the ADR-0012 confirmation (a restore gets no exemption)', () => {
  it('opens the shared lost-content dialog rather than failing quietly', async () => {
    const { PublishLostContentError } = await import('../../../lib/api');
    restoreMutate.mockRejectedValueOnce(new PublishLostContentError(LOST));
    renderPanel(5);

    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));
    fireEvent.click(await screen.findByTestId('codex-restore-confirm'));

    await screen.findByTestId('codex-lost-content-dialog');
    expect(screen.getByTestId('codex-lost-content-count')).toHaveTextContent('2 preserved items');
  });

  it('re-sends the restore with exactly the acknowledged ids', async () => {
    // The whole point of the confirmation: what the reader agreed to is what
    // travels. A retry without the ids would be refused again; a retry with
    // ids the reader never saw would defeat ADR-0012 entirely.
    const { PublishLostContentError } = await import('../../../lib/api');
    restoreMutate.mockRejectedValueOnce(new PublishLostContentError(LOST));
    renderPanel(5);

    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));
    fireEvent.click(await screen.findByTestId('codex-restore-confirm'));
    fireEvent.click(await screen.findByTestId('codex-lost-content-confirm'));

    await waitFor(() => expect(restoreMutate).toHaveBeenCalledTimes(2));
    expect(restoreMutate.mock.calls[1][0]).toMatchObject({
      version: 2,
      base_version: 5,
      acknowledged_lost_ids: ['u2', 'u5'],
    });
  });

  it('sends nothing more if the reader cancels', async () => {
    const { PublishLostContentError } = await import('../../../lib/api');
    restoreMutate.mockRejectedValueOnce(new PublishLostContentError(LOST));
    renderPanel(5);

    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));
    fireEvent.click(await screen.findByTestId('codex-restore-confirm'));
    fireEvent.click(await screen.findByTestId('codex-lost-content-cancel'));

    await waitFor(() =>
      expect(screen.queryByTestId('codex-lost-content-dialog')).not.toBeInTheDocument(),
    );
    expect(restoreMutate).toHaveBeenCalledTimes(1);
  });
});

describe('the version conflict', () => {
  const CONFLICT = {
    page_id: 'page-1',
    expected_version: 5,
    current_page: {
      id: 'page-1',
      space_id: 'space-1',
      title: 'Runbook',
      content: '',
      doc: null,
      version: 9,
      parent_id: null,
      author_id: 'u1',
      path: 'runbook',
      created_at: '',
      updated_at: '',
    },
    message: 'This page was published again while you were deciding…',
  };

  it('re-sends the same version with overwrite once the reader replaces theirs', async () => {
    const { PublishConflictError } = await import('../../../lib/api');
    restoreMutate.mockRejectedValueOnce(new PublishConflictError(CONFLICT));
    renderPanel(5);

    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));
    fireEvent.click(await screen.findByTestId('codex-restore-confirm'));
    fireEvent.click(await screen.findByTestId('codex-conflict-overwrite'));

    await waitFor(() => expect(restoreMutate).toHaveBeenCalledTimes(2));
    expect(restoreMutate.mock.calls[1][0]).toMatchObject({
      version: 2,
      base_version: 5,
      overwrite: true,
    });
  });

  it('reloading refreshes the history and adopts the version the server named', async () => {
    const { PublishConflictError } = await import('../../../lib/api');
    restoreMutate.mockRejectedValueOnce(new PublishConflictError(CONFLICT));
    renderPanel(5);

    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));
    fireEvent.click(await screen.findByTestId('codex-restore-confirm'));
    fireEvent.click(await screen.findByTestId('codex-conflict-reload'));

    await waitFor(() => expect(refetchSpy).toHaveBeenCalled());
    expect(await screen.findByTestId('codex-restore-notice')).toHaveTextContent('version 9');

    // A second attempt is guarded against 9, not against the stale 5 — without
    // that it would be refused identically and the reload would lead nowhere.
    fireEvent.click(screen.getByRole('button', { name: 'Restore version 2' }));
    fireEvent.click(await screen.findByTestId('codex-restore-confirm'));
    await waitFor(() => expect(restoreMutate).toHaveBeenCalledTimes(2));
    expect(restoreMutate.mock.calls[1][0]).toMatchObject({ version: 2, base_version: 9 });
  });
});
