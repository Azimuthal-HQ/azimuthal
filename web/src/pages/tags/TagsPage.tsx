/**
 * The org tag vocabulary, as a browsable index — the module-neutral surface
 * the old Vector "Labels" nav item now opens.
 *
 * ## Why this lives at /labels
 *
 * Vector's sidebar linked to a Labels page that was a hard-coded coming-soon
 * for four releases, promising a label manager that was never built; the
 * entity-tags convergence delivered the feature under its real name instead.
 * The route is kept so a person who bookmarked it lands somewhere that now
 * actually works, and the surface it opens is this one: every tag in the
 * organisation, each linking to the cross-module browse of what carries it.
 *
 * ## What it deliberately does not say
 *
 * No per-tag usage counts. The browse behind each link filters to the spaces
 * the READER can see, so any number printed here would either disagree with
 * the next page or leak how much use a tag gets in spaces the reader cannot
 * open. The vocabulary itself is org-visible by design — a tag NAME is not a
 * space's secret; the entities carrying it are filtered separately.
 */
import { Link, useParams } from 'react-router-dom';
import { Tag as TagIcon, Tags } from 'lucide-react';

import { EmptyState } from '../../shell/EmptyState';
import { friendlyErrorMessage, useOrgTags } from '../../lib/api';
import { getCurrentOrgId } from '../../lib/auth';
import { tagBrowsePath } from '../../components/tags/tagLinks';

export function TagsPage() {
  const orgId = getCurrentOrgId();
  const { module = 'vector', spaceId = '' } = useParams<{ module: string; spaceId: string }>();
  const { data: tags = [], isLoading, error } = useOrgTags(orgId);

  return (
    <div className="space-y-4" data-testid="tags-index-page">
      <div>
        <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
          Tags
        </h1>
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Every tag in this organisation. Tags are shared by pages, tickets and project items, and
          are created by use — tag anything, and the tag exists.
        </p>
      </div>

      {isLoading ? (
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">Loading tags…</p>
      ) : error ? (
        <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
          {friendlyErrorMessage(error, 'The tags could not be loaded.')}
        </p>
      ) : tags.length === 0 ? (
        <EmptyState
          icon={Tags}
          title="No tags yet"
          description="Tag a page, a ticket, or a project item and it will appear here. Tags have no set-up step — typing one creates it."
        />
      ) : (
        <ul className="flex flex-wrap gap-2" data-testid="tags-index-list">
          {tags.map((tag) => (
            <li key={tag.id}>
              <Link
                to={tagBrowsePath(module, spaceId, tag.name)}
                data-testid="tags-index-tag"
                data-slug={tag.slug}
                className="inline-flex items-center gap-1.5 rounded-full border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1 text-[var(--text-sm)] text-[var(--color-text)] transition-colors hover:border-[var(--module-codex)] hover:bg-[color-mix(in_srgb,var(--module-codex)_12%,transparent)]"
              >
                <TagIcon className="h-3.5 w-3.5 text-[var(--color-text-muted)]" aria-hidden="true" />
                {tag.name}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
