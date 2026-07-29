/**
 * The tag browse: every page carrying one tag, across every space the reader
 * can see (U4).
 *
 * ## Why every row names its space
 *
 * A tag is org-scoped and its results cross space boundaries, so this is the
 * one Codex list where two rows can legitimately read `Runbook`. A bare title
 * list would make those two indistinguishable, and picking the wrong one costs
 * the reader a navigation and a puzzled moment. Each row therefore carries the
 * space it lives in, and each link goes to that page's OWN space — never to the
 * space in the URL, which describes where the reader came from rather than
 * where the results are.
 *
 * ## Why the empty state is careful about what it claims
 *
 * The server filters these results to the spaces the reader can read, so an
 * empty list means "nothing you can see", not "nothing". Saying the tag is
 * unused would be a statement about other people's spaces that this page has
 * no standing to make — and it is exactly the wrong thing to tell somebody
 * whose colleague has just told them the tag is on twenty pages.
 */
import { Link, useParams } from 'react-router-dom';
import { AlertCircle, FileText, Tag as TagIcon } from 'lucide-react';

import { codexMeasureClasses } from '../../components/codex/editorStyles';
import { friendlyErrorMessage, usePagesWithTag } from '../../lib/api';
import { getCurrentOrgId } from '../../lib/auth';

export function TagPage() {
  // React Router has already percent-decoded the segment, so `label` is the
  // display form `tagBrowsePath` encoded — and it is a LABEL, not a slug. The
  // server slugifies whatever it is given; see `tagLinks.ts` for why the client
  // must not do that itself.
  const { label = '' } = useParams<{ spaceId: string; label: string }>();
  const orgId = getCurrentOrgId();
  const { data, isLoading, error } = usePagesWithTag(orgId, label);

  if (isLoading) {
    return (
      <p className="p-6 text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading pages…</p>
    );
  }

  if (error) {
    return (
      <div className={`${codexMeasureClasses} p-6`} data-testid="codex-tag-page">
        <div className="flex items-start gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-[var(--color-danger)]" aria-hidden="true" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {error.status === 404
              ? `There is no tag called “${label}”.`
              : friendlyErrorMessage(error, 'The pages with this tag could not be loaded.')}
          </p>
        </div>
      </div>
    );
  }

  const pages = data?.pages ?? [];

  return (
    <div className={`${codexMeasureClasses} p-6`} data-testid="codex-tag-page">
      <div className="mb-6">
        <h1 className="flex items-center gap-2 text-[22px] font-semibold leading-[1.3] tracking-[-.01em] text-[var(--color-text)]">
          <TagIcon className="h-5 w-5 text-[var(--module-codex)]" aria-hidden="true" />
          {data?.tag.name ?? label}
        </h1>
        <p className="mt-1.5 text-[var(--text-sm)] text-[var(--color-text-muted)]">
          {data?.truncated
            ? // The count is a floor, not a total, so it must not be phrased as
              // one. A capped list reads as complete unless the surface says
              // otherwise, and the pages left out are the least recently
              // updated — the ones somebody hunting for an old page wants.
              `The ${pages.length} most recently updated pages carrying this tag`
            : pages.length === 1
              ? '1 page carries this tag'
              : `${pages.length} pages carry this tag`}
          , across every space you can read.
        </p>
        {data?.truncated && (
          <p
            data-testid="codex-tag-page-truncated"
            className="mt-1.5 text-[var(--text-xs)] text-[var(--color-warning)]"
          >
            This tag is on more pages than can be listed here. Search within a space to narrow it
            down.
          </p>
        )}
      </div>

      {pages.length === 0 ? (
        <div className="flex min-h-[200px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)] p-6">
          <p className="max-w-[46ch] text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
            This tag exists, but no page you can see carries it. Results are limited to the spaces
            you have access to, so pages in other spaces may be tagged with it.
          </p>
        </div>
      ) : (
        <ul className="divide-y divide-[var(--color-border)] rounded-[var(--radius-lg)] border border-[var(--color-border)]">
          {pages.map((page) => (
            <li key={page.page_id}>
              <Link
                to={`/codex/${page.space_id}/pages/${page.page_id}`}
                data-testid="codex-tag-page-row"
                className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-[var(--color-surface-hover)]"
              >
                <FileText
                  className="mt-0.5 h-4 w-4 shrink-0 text-[var(--color-text-muted)]"
                  aria-hidden="true"
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                    {page.title}
                  </span>
                  <span className="mt-0.5 block truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">
                    {page.space_name}
                    <span className="mx-1.5" aria-hidden="true">
                      ·
                    </span>
                    Updated {(page.updated_at ?? '').slice(0, 10)}
                  </span>
                </span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
