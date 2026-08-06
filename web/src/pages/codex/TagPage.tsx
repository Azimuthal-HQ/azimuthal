/**
 * The tag browse: every entity carrying one tag, across every space the reader
 * can see (U4, generalized by the entity-tags convergence).
 *
 * ## Why every row names its space and its kind
 *
 * A tag is org-scoped and its results cross space and module boundaries, so
 * this is the one list where two rows can legitimately read `Runbook` — and
 * one of them can be a page while the other is a ticket. A bare title list
 * would make those indistinguishable, and picking the wrong one costs the
 * reader a navigation and a puzzled moment. Each row therefore carries the
 * space it lives in, its kind, and its human reference where it has one, and
 * each link goes to that entity's OWN module and space — never to the ones in
 * the URL, which describe where the reader came from rather than where the
 * results are.
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
import { AlertCircle, FileText, LifeBuoy, ListTodo, Tag as TagIcon } from 'lucide-react';

import { codexMeasureClasses } from '../../components/codex/editorStyles';
import { friendlyErrorMessage, useEntitiesWithTag } from '../../lib/api';
import type { TaggedEntity } from '../../lib/api';
import { getCurrentOrgId } from '../../lib/auth';

/**
 * Where a browse row links to: the entity's own detail surface, in its own
 * module's chrome. Items are addressed by item_key — that is what their detail
 * route takes — and the ref IS the item_key, composed server-side.
 */
function entityPath(e: TaggedEntity): string {
  switch (e.entity_type) {
    case 'ticket':
      return `/beacon/${e.space_id}/tickets/${e.entity_id}`;
    case 'project_item':
      return `/vector/${e.space_id}/backlog/${encodeURIComponent(e.ref)}`;
    default:
      return `/codex/${e.space_id}/pages/${e.entity_id}`;
  }
}

/** The row icon, by kind. The same icons the modules use for themselves. */
function EntityIcon({ type }: { type: TaggedEntity['entity_type'] }) {
  const classes = 'mt-0.5 h-4 w-4 shrink-0 text-[var(--color-text-muted)]';
  switch (type) {
    case 'ticket':
      return <LifeBuoy className={classes} aria-hidden="true" />;
    case 'project_item':
      return <ListTodo className={classes} aria-hidden="true" />;
    default:
      return <FileText className={classes} aria-hidden="true" />;
  }
}

export function TagPage() {
  // React Router has already percent-decoded the segment, so `label` is the
  // display form `tagBrowsePath` encoded — and it is a LABEL, not a slug. The
  // server slugifies whatever it is given; see `tagLinks.ts` for why the client
  // must not do that itself.
  const { label = '' } = useParams<{ spaceId: string; label: string }>();
  const orgId = getCurrentOrgId();
  const { data, isLoading, error } = useEntitiesWithTag(orgId, label);

  if (isLoading) {
    return (
      <p className="p-6 text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading…</p>
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
              : friendlyErrorMessage(error, 'The entities with this tag could not be loaded.')}
          </p>
        </div>
      </div>
    );
  }

  const entities = data?.entities ?? [];

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
              // otherwise, and the entities left out are the least recently
              // updated — the ones somebody hunting for an old page wants.
              `The ${entities.length} most recently updated pages, tickets and items carrying this tag`
            : entities.length === 1
              ? '1 page, ticket or item carries this tag'
              : `${entities.length} pages, tickets and items carry this tag`}
          , across every space you can read.
        </p>
        {data?.truncated && (
          <p
            data-testid="codex-tag-page-truncated"
            className="mt-1.5 text-[var(--text-xs)] text-[var(--color-warning)]"
          >
            This tag is on more entities than can be listed here. Search within a space to narrow
            it down.
          </p>
        )}
      </div>

      {entities.length === 0 ? (
        <div className="flex min-h-[200px] items-center justify-center rounded-[var(--radius-lg)] border-2 border-dashed border-[var(--color-border)] p-6">
          <p className="max-w-[46ch] text-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
            This tag exists, but nothing you can see carries it. Results are limited to the spaces
            you have access to, so pages, tickets or items in other spaces may be tagged with it.
          </p>
        </div>
      ) : (
        <ul className="divide-y divide-[var(--color-border)] rounded-[var(--radius-lg)] border border-[var(--color-border)]">
          {entities.map((entity) => (
            <li key={`${entity.entity_type}-${entity.entity_id}`}>
              <Link
                to={entityPath(entity)}
                data-testid="codex-tag-page-row"
                data-entity-type={entity.entity_type}
                className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-[var(--color-surface-hover)]"
              >
                <EntityIcon type={entity.entity_type} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-[var(--text-sm)] font-medium text-[var(--color-text)]">
                    {entity.title}
                  </span>
                  <span className="mt-0.5 block truncate text-[var(--text-xs)] text-[var(--color-text-muted)]">
                    {/* Tickets and items lead with their stable human ref
                        (BEA-42, VEC-14). A page's ref is its path — ancestry a
                        row does not need when the space is already named. */}
                    {entity.entity_type !== 'page' && entity.ref ? (
                      <>
                        <span className="font-mono">{entity.ref}</span>
                        <span className="mx-1.5" aria-hidden="true">
                          ·
                        </span>
                      </>
                    ) : null}
                    {entity.space_name}
                    <span className="mx-1.5" aria-hidden="true">
                      ·
                    </span>
                    Updated {(entity.updated_at ?? '').slice(0, 10)}
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
