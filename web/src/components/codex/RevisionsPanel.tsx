/**
 * A page's history, and the two things a reader actually wants from one:
 * to see what changed between two versions, and to put an earlier one back.
 *
 * ## What the panel was
 *
 * The version and a date. The author was already in the data — `author_id` has
 * been on `page_revisions` since migration 005 — and merely absent from the
 * read, so a history that could not say who wrote anything was a gap in the
 * surface rather than in the schema. `author_name` closes it, and it is
 * nullable: a removed account must not make its revisions vanish from a page,
 * so those rows render "Unknown" rather than being hidden or given an invented
 * name.
 *
 * ## Why the comparison is called a text comparison
 *
 * The server diffs the markdown projection of two revisions, not their
 * documents. That is what makes a page written before the document editor and
 * one written after it comparable at all — both project to markdown, and only
 * one of them has a document. It also means the comparison genuinely does not
 * know about structure: a table that gained a column and a paragraph that
 * gained a sentence arrive here as the same kind of change. The UI says so, in
 * those words, because a diff that silently under-reports a structural change
 * while looking authoritative is worse than one that admits its scope. A
 * structural comparison of rich documents is future work.
 *
 * ## Why a restore is a publish, and gets no exemption
 *
 * `RestoreRevision` republishes the older content through the ordinary publish
 * path, so it meets the ordinary publish refusals: the version guard, and
 * ADR-0012's lost-preserved-content confirmation. The second one is not an edge
 * case here — an older version usually lacks preserved content the current one
 * has, which is part of what makes it older — so restoring is the *most* likely
 * way to remove content the editor cannot display. Both refusals are answered
 * with the dialogs the editor already uses, from `PublishDialogs`: a second
 * conflict dialogue would be a second place for that wording to drift, and
 * ADR-0012's confirmation is not something a restore may route around.
 */
import { useState } from 'react';
import { ChevronRight, RotateCcw, X } from 'lucide-react';

import {
  PublishConflictError,
  PublishLostContentError,
  friendlyErrorMessage,
  useRestoreWikiRevision,
  useWikiDiff,
  useWikiRevisions,
} from '../../lib/api';
import type {
  CodexPublishConflict,
  CodexPublishLostContent,
  WikiDiffSegment,
} from '../../lib/api';
import { cn } from '../../lib/utils';
import { Button } from '../ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '../ui/dialog';
import { LostContentDialog, PublishConflictDialog } from './PublishDialogs';

interface RevisionsPanelProps {
  spaceId: string;
  pageId: string;
  currentVersion: number;
  onClose: () => void;
}

/** The refusal being answered, if any. One at a time, as in the editor. */
type Refusal =
  | { kind: 'conflict'; detail: CodexPublishConflict }
  | { kind: 'lost'; detail: CodexPublishLostContent }
  | null;

export function RevisionsPanel({ spaceId, pageId, currentVersion, onClose }: RevisionsPanelProps) {
  const { data: revisions = [], isLoading, refetch } = useWikiRevisions(spaceId, pageId);
  const restore = useRestoreWikiRevision(spaceId, pageId);

  /**
   * The versions picked for comparison, in the order they were clicked.
   *
   * Two clicks make a range and a third starts over, so the reader never has to
   * find a "clear" affordance to ask a different question. Clicking the one
   * selected row deselects it, which is the behaviour the single-selection
   * panel this replaces already had.
   */
  const [selected, setSelected] = useState<number[]>([]);

  /** The version whose restore is awaiting confirmation, or being refused. */
  const [confirming, setConfirming] = useState<number | null>(null);
  const [restoringVersion, setRestoringVersion] = useState<number | null>(null);
  const [refusal, setRefusal] = useState<Refusal>(null);
  /**
   * Carried forward exactly as the editor carries it: an overwrite that is then
   * refused for lost content has to keep the decision, or the republish hits
   * the version guard again and the two dialogs bounce off each other forever.
   */
  const [overwriteAgreed, setOverwriteAgreed] = useState(false);
  /**
   * The version this panel believes is current, once the server has told us
   * otherwise. `currentVersion` comes from the page the parent loaded, and a
   * conflict means that page moved on; reloading adopts the version the
   * conflict body names so a second attempt has somewhere to go. Cleared on
   * success, after which the prop governs again.
   */
  const [adoptedVersion, setAdoptedVersion] = useState<number | null>(null);
  const guardVersion = adoptedVersion ?? currentVersion;

  /** A success that is not a new version: the page already held that content. */
  const [notice, setNotice] = useState<string | null>(null);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  // Ordered, so "compare these two" does not depend on which was clicked first.
  const [from, to] = [...selected].sort((a, b) => a - b);
  // The hook refuses a from === to comparison itself, and stays disabled while
  // fewer than two versions are picked.
  const diff = useWikiDiff(spaceId, pageId, from ?? 0, to ?? 0);

  function toggle(version: number) {
    setSelected((current) => {
      if (current.length >= 2) return [version];
      if (current.includes(version)) return current.filter((v) => v !== version);
      return [...current, version];
    });
  }

  async function doRestore(
    version: number,
    options: { overwrite?: boolean; acknowledgedLostIds?: string[] } = {},
  ) {
    setErrorMessage(null);
    setNotice(null);
    setConfirming(null);
    setRestoringVersion(version);
    const overwrite = options.overwrite ?? overwriteAgreed;

    try {
      const result = await restore.mutateAsync({
        version,
        base_version: guardVersion,
        // Absent rather than empty: the server decodes with
        // DisallowUnknownFields and "I acknowledge nothing" must mean exactly
        // that, which is the same reason the editor builds its body this way.
        ...(options.acknowledgedLostIds?.length
          ? { acknowledged_lost_ids: options.acknowledgedLostIds }
          : {}),
        ...(overwrite ? { overwrite: true } : {}),
      });
      setRefusal(null);
      setOverwriteAgreed(false);
      setAdoptedVersion(null);
      setRestoringVersion(null);
      if (result.restored) {
        setNotice(`Version ${version} was published as a new version. The history is unchanged.`);
        return;
      }
      // A 200, not an error envelope: nothing failed, the page simply already
      // held that content. The body's sentence is prose written for a person
      // and names the version involved — the same deliberate exception to
      // shared-surfaces.md section 2 that the publish dialogs document — so it
      // is shown as given rather than restated here and left to drift.
      setNotice(
        result.message ??
          `Version ${version} is already this page's content, so there was nothing to restore.`,
      );
    } catch (err) {
      // `restoringVersion` is left set: a refusal dialog's answer re-calls this
      // for the same version, and losing it would send the retry nowhere.
      if (err instanceof PublishConflictError) {
        setRefusal({ kind: 'conflict', detail: err.detail });
        return;
      }
      if (err instanceof PublishLostContentError) {
        setRefusal({ kind: 'lost', detail: err.detail });
        return;
      }
      setRestoringVersion(null);
      setErrorMessage(friendlyErrorMessage(err, 'That version was not restored.'));
    }
  }

  /**
   * The conflict dialog's reload, in a panel with no draft to keep: refetch the
   * history so the new version appears in it, and adopt that version as the
   * guard so restoring again is answered rather than refused identically.
   */
  function reloadHistory(detail: CodexPublishConflict) {
    setRefusal(null);
    setOverwriteAgreed(false);
    setAdoptedVersion(detail.current_page.version);
    setNotice(
      `This page is now at version ${detail.current_page.version}. The history below has been refreshed.`,
    );
    void refetch();
  }

  const busy = restore.isPending;

  return (
    <div
      data-testid="codex-revisions-panel"
      className="flex h-full flex-col border-l border-[var(--color-border)] bg-[var(--color-surface)]"
    >
      <div className="flex items-center justify-between border-b border-[var(--color-border)] px-4 py-3">
        <h3 className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">Revision History</h3>
        <button
          onClick={onClose}
          aria-label="Close revision history"
          data-testid="codex-revisions-close"
          className="rounded p-1 text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {isLoading ? (
        <div className="flex flex-1 items-center justify-center text-[var(--color-text-muted)] text-[var(--text-sm)]">Loading…</div>
      ) : revisions.length === 0 ? (
        <div className="flex flex-1 items-center justify-center text-[var(--color-text-muted)] text-[var(--text-sm)]">No revisions yet.</div>
      ) : (
        <div className="flex flex-1 flex-col overflow-hidden">
          <p className="border-b border-[var(--color-border)] px-4 py-2 text-[var(--text-xs)] text-[var(--color-text-muted)]">
            Pick two versions to compare them.
          </p>

          <div className="overflow-y-auto border-b border-[var(--color-border)]" style={{ maxHeight: '50%' }}>
            {revisions.map((rev) => {
              const isSelected = selected.includes(rev.version);
              return (
                <div
                  key={rev.id}
                  data-testid="codex-revision-row"
                  data-version={rev.version}
                  className={cn(
                    'flex items-start gap-1 border-b border-[var(--color-border)] transition-colors last:border-0',
                    isSelected ? 'bg-[var(--color-primary-muted)]' : 'hover:bg-[var(--color-surface-hover)]',
                  )}
                >
                  <button
                    type="button"
                    data-testid="codex-revision-select"
                    data-version={rev.version}
                    aria-pressed={isSelected}
                    onClick={() => toggle(rev.version)}
                    className="flex min-w-0 flex-1 items-start gap-2 px-4 py-2.5 text-left"
                  >
                    <ChevronRight
                      aria-hidden="true"
                      className={cn(
                        'mt-0.5 h-3.5 w-3.5 shrink-0 text-[var(--color-text-muted)] transition-transform',
                        isSelected && 'rotate-90',
                      )}
                    />
                    <span className="min-w-0">
                      <span className="block text-[var(--text-sm)] text-[var(--color-text)]">
                        v{rev.version}
                        {rev.version === currentVersion && (
                          <span className="ml-2 rounded-full bg-[var(--color-primary-muted)] px-1.5 py-0.5 text-[var(--text-xs)] text-[var(--color-primary)]">current</span>
                        )}
                      </span>
                      <span className="block text-[var(--text-xs)] text-[var(--color-text-muted)]">
                        {/* "Unknown" when the account has been removed. The row
                            stays in the history either way — deleting a person
                            does not delete what they wrote — and naming the
                            absence is honest where inventing an author is not. */}
                        <span data-testid="codex-revision-author">{rev.author_name ?? 'Unknown'}</span>
                        {' · '}
                        {/* The ISO prefix rather than a locale date: it is what
                            the panel has always shown, and it does not shift a
                            revision across a day boundary by timezone. */}
                        {(rev.created_at ?? '').slice(0, 10)}
                      </span>
                    </span>
                  </button>

                  {/* No restore on the version the page already holds: it is the
                      no-op case, and offering it would invite a click whose only
                      possible answer is "there was nothing to restore". */}
                  {rev.version !== currentVersion && (
                    <button
                      type="button"
                      data-testid="codex-revision-restore"
                      data-version={rev.version}
                      aria-label={`Restore version ${rev.version}`}
                      disabled={busy}
                      onClick={() => setConfirming(rev.version)}
                      className="mr-2 mt-2 shrink-0 rounded-[var(--radius-md)] p-1.5 text-[var(--color-text-muted)] transition-colors hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-text)] disabled:opacity-50"
                    >
                      <RotateCcw className="h-3.5 w-3.5" aria-hidden="true" />
                    </button>
                  )}
                </div>
              );
            })}
          </div>

          <div className="flex-1 overflow-y-auto p-4">
            {notice && (
              <p
                data-testid="codex-restore-notice"
                role="status"
                className="mb-3 rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[color-mix(in_srgb,var(--color-success)_10%,transparent)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]"
              >
                {notice}
              </p>
            )}
            {errorMessage && (
              <p
                data-testid="codex-restore-error"
                role="alert"
                className="mb-3 rounded-[var(--radius-md)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-danger)]"
              >
                {errorMessage}
              </p>
            )}

            {selected.length < 2 ? (
              <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                {selected.length === 1
                  ? `Version ${selected[0]} selected. Pick a second version to compare it with.`
                  : 'No versions selected.'}
              </p>
            ) : diff.isLoading ? (
              <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Comparing…</p>
            ) : diff.error ? (
              <p data-testid="codex-diff-error" role="alert" className="text-[var(--text-sm)] text-[var(--color-danger)]">
                {friendlyErrorMessage(diff.error, 'Those two versions could not be compared.')}
              </p>
            ) : diff.data ? (
              <div data-testid="codex-revision-diff">
                <h4 className="text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
                  Text comparison
                </h4>
                <p className="mb-3 mt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  Comparing v{diff.data.from_version} with v{diff.data.to_version}. This is a text
                  comparison: it compares the two versions&rsquo; text, not their structure, so a
                  change to a table, a layout or an embedded block shows up as changed text rather
                  than as a change to the thing itself.
                </p>

                {/* Only when the title actually changed — the server sends no
                    segments when it did not, and a title row that always
                    appeared would read as a change every time. */}
                {diff.data.title_segments && diff.data.title_segments.length > 0 && (
                  <div className="mb-3" data-testid="codex-diff-title">
                    <p className="mb-1 text-[var(--text-xs)] font-semibold uppercase tracking-wide text-[var(--color-text-muted)]">
                      Title
                    </p>
                    <DiffRuns segments={diff.data.title_segments} />
                  </div>
                )}

                {diff.data.content_segments && diff.data.content_segments.length > 0 ? (
                  <DiffRuns segments={diff.data.content_segments} />
                ) : (
                  <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
                    The text of these two versions is identical.
                  </p>
                )}
              </div>
            ) : null}
          </div>
        </div>
      )}

      {/* Confirmed before it happens, and the confirmation says why it is safe:
          the property that makes a restore recoverable is that it appends. */}
      {confirming !== null && (
        <Dialog open onOpenChange={(open) => !open && setConfirming(null)}>
          <DialogContent data-testid="codex-restore-confirm-dialog">
            <DialogHeader>
              <DialogTitle>Restore version {confirming}?</DialogTitle>
              <DialogDescription>
                This publishes version {confirming}&rsquo;s content as a <strong>new</strong>{' '}
                version. Nothing is rewritten and nothing is removed from the history — every
                version since stays exactly as it is, so the restore itself can be undone by
                restoring one of them.
              </DialogDescription>
            </DialogHeader>
            <DialogFooter className="gap-2">
              <Button variant="outline" onClick={() => setConfirming(null)} data-testid="codex-restore-cancel">
                Cancel
              </Button>
              <Button
                onClick={() => void doRestore(confirming)}
                disabled={busy}
                data-testid="codex-restore-confirm"
              >
                {busy ? 'Restoring…' : `Restore version ${confirming}`}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}

      {refusal?.kind === 'conflict' && (
        <PublishConflictDialog
          detail={refusal.detail}
          busy={busy}
          onReload={() => reloadHistory(refusal.detail)}
          onOverwrite={() => {
            setOverwriteAgreed(true);
            if (restoringVersion !== null) void doRestore(restoringVersion, { overwrite: true });
          }}
          onCancel={() => setRefusal(null)}
        />
      )}

      {refusal?.kind === 'lost' && (
        <LostContentDialog
          detail={refusal.detail}
          busy={busy}
          onConfirm={() => {
            if (restoringVersion !== null) {
              void doRestore(restoringVersion, { acknowledgedLostIds: refusal.detail.lost_ids });
            }
          }}
          onCancel={() => setRefusal(null)}
        />
      )}
    </div>
  );
}

/**
 * One diff, as runs of text.
 *
 * Colour is not the only signal: removed runs are `<del>` and added runs are
 * `<ins>`, which carry the meaning in the markup for anything that is not
 * looking at the colours — a screen reader, a printout, a reader who cannot
 * distinguish the two hues. The tint is the second signal, not the first.
 */
function DiffRuns({ segments }: { segments: WikiDiffSegment[] }) {
  return (
    <p className="whitespace-pre-wrap break-words font-mono text-[var(--text-xs)] leading-relaxed text-[var(--color-text)]">
      {segments.map((segment, index) =>
        segment.op === -1 ? (
          <del
            key={index}
            data-testid="codex-diff-removed"
            className="bg-[color-mix(in_srgb,var(--color-danger)_18%,transparent)] text-[var(--color-text)]"
          >
            {segment.text}
          </del>
        ) : segment.op === 1 ? (
          <ins
            key={index}
            data-testid="codex-diff-added"
            className="bg-[color-mix(in_srgb,var(--color-success)_18%,transparent)] text-[var(--color-text)]"
          >
            {segment.text}
          </ins>
        ) : (
          <span key={index} data-testid="codex-diff-unchanged">
            {segment.text}
          </span>
        ),
      )}
    </p>
  );
}
