/**
 * The Codex Drafts view — the pages in this space on which the viewer holds
 * unpublished work.
 *
 * It exists because the data does. `GET …/wiki/drafts` shipped in PR #73, the
 * index `page_drafts (author_id)` was added for exactly this lookup (migration
 * 036), and the sidebar has linked here since the navigation collapse — the
 * route was the last placeholder standing.
 *
 * Author-scoped by the query itself, not by a filter applied afterwards: a
 * draft is visible to nobody but the person who wrote it.
 */
import { useParams } from 'react-router-dom';
import { Link } from 'react-router-dom';
import { AlertTriangle, FileText, PenLine } from 'lucide-react';

import { friendlyErrorMessage, useSpaceDrafts } from '../../lib/api';

export function DraftsPage() {
  const { spaceId = '' } = useParams<{ spaceId: string }>();
  const { data: drafts = [], isLoading, error } = useSpaceDrafts(spaceId);

  if (isLoading) {
    return (
      <p className="p-6 text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading drafts…</p>
    );
  }

  if (error) {
    return (
      <p data-testid="codex-drafts-error" className="p-6 text-[var(--text-sm)] text-[var(--color-danger)]">
        {friendlyErrorMessage(error, 'Your drafts are not available right now.')}
      </p>
    );
  }

  return (
    <div className="mx-auto w-full max-w-[76ch] p-6" data-testid="codex-drafts">
      <div className="mb-6">
        <h1 className="flex items-center gap-2 text-[22px] font-semibold leading-[1.3] tracking-[-.01em] text-[var(--color-text)]">
          <PenLine className="h-5 w-5 text-[var(--module-codex)]" aria-hidden="true" />
          Drafts
        </h1>
        <p className="mt-1.5 text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Pages you have edited but not published. Only you can see these — readers see the last
          published version.
        </p>
      </div>

      {drafts.length === 0 ? (
        <div className="flex min-h-[200px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)]">
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            You have no unpublished drafts in this space.
          </p>
        </div>
      ) : (
        <ul className="divide-y divide-[var(--color-border)] rounded-[var(--radius-lg)] border border-[var(--color-border)]">
          {drafts.map((draft) => (
            <li key={draft.page_id}>
              <Link
                to={`/codex/${spaceId}/pages/${draft.page_id}`}
                data-testid="codex-draft-row"
                className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-[var(--color-surface-hover)]"
              >
                <FileText
                  className="mt-0.5 h-4 w-4 shrink-0 text-[var(--color-text-muted)]"
                  aria-hidden="true"
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                    {draft.draft_title || draft.page_title}
                  </span>
                  {draft.draft_title !== draft.page_title && (
                    <span className="block truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">
                      Published as “{draft.page_title}”
                    </span>
                  )}
                  <span className="mt-0.5 block text-[var(--text-xs)] text-[var(--color-text-muted)]">
                    Edited {(draft.updated_at ?? '').slice(0, 10)}
                  </span>
                </span>
                {draft.stale && (
                  /* The page moved on since this draft started, so publishing
                     it will hit the version guard. Saying so here means the
                     conflict dialog is not the first the author hears of it. */
                  <span
                    data-testid="codex-draft-stale"
                    className="flex shrink-0 items-center gap-1 rounded-[var(--radius-md)] bg-[color-mix(in_srgb,var(--color-warning)_18%,transparent)] px-2 py-0.5 text-[var(--text-xs)] text-[var(--color-warning)]"
                  >
                    <AlertTriangle className="h-3 w-3" aria-hidden="true" />
                    Page updated since
                  </span>
                )}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
