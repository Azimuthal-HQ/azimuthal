import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Link2, Trash2 } from 'lucide-react';
import { cn } from '../lib/utils';
import { Input } from './ui/input';
import { PageRefField } from './PageRefField';
import {
  RELATION_KINDS,
  useRelations,
  useCreateRelation,
  useDeleteRelation,
  useItemSearch,
  type Relation,
  type RelationEntityType,
} from '../lib/api';

export interface RelationsSectionProps {
  orgId: string;
  spaceId: string;
  /** The entity whose panel this is — the near side of every relation shown. */
  entityType: Extract<RelationEntityType, 'project_item' | 'ticket'>;
  entityId: string;
}

/**
 * The relations panel (A4): one surface for every entity type that renders
 * relations, listed on `docs/design/shared-surfaces.md` terms — this used to
 * be an inline block in ItemDetailPage, and giving tickets the feature by
 * copying that block would have been the second implementation that page
 * calls a defect.
 *
 * Target pickers by near side:
 *  - a project item links to another WORK ITEM (the in-space search this
 *    section has always had) or to a PAGE (the org-wide suggest typeahead);
 *  - a ticket links to a PAGE. Ticket-to-ticket linking has no picker yet —
 *    the API accepts it, and a picker can join this select without reshaping
 *    anything here.
 *
 * A readable far side is a LINK to the entity it names, built from far_type +
 * far_space_id — the far side's OWN space, because relations cross spaces and
 * this panel's space is not a substitute. An unreadable far side stays the
 * identity-free placeholder row (D82): the panel may say a link exists, and
 * nothing more.
 */
export function RelationsSection({ orgId, spaceId, entityType, entityId }: RelationsSectionProps) {
  const { data: relations = [] } = useRelations(spaceId, entityType, entityId);
  const createRelationMutation = useCreateRelation(spaceId, entityType, entityId);
  const deleteRelationMutation = useDeleteRelation(spaceId, entityType, entityId);
  const [relKind, setRelKind] = useState('relates_to');

  // Which picker the add-row shows. Items default to their historical item
  // search; tickets have only the page picker today.
  const targetChoices: Array<{ value: 'project_item' | 'page'; label: string }> =
    entityType === 'project_item'
      ? [
          { value: 'project_item', label: 'Work item' },
          { value: 'page', label: 'Page' },
        ]
      : [{ value: 'page', label: 'Page' }];
  const [targetType, setTargetType] = useState<'project_item' | 'page'>(targetChoices[0].value);

  // The hand-rolled in-space item search, kept as-is for the Work item
  // target. It predates PageRefField and duplicates part of its shape; the
  // extraction was weighed and skipped — a shared picker would have to span
  // "filter a space-scoped list" and "query an org-scoped typeahead", and a
  // partially-shared picker is worse than two honest ones.
  const [relSearch, setRelSearch] = useState('');
  const [relSearchDebounced, setRelSearchDebounced] = useState('');
  const { data: searchResults = [] } = useItemSearch(spaceId, relSearchDebounced);

  type DebouncedRelSearch = typeof handleRelSearchChange & {
    _t?: ReturnType<typeof setTimeout>;
  };

  function handleRelSearchChange(v: string) {
    setRelSearch(v);
    clearTimeout((handleRelSearchChange as DebouncedRelSearch)._t);
    (handleRelSearchChange as DebouncedRelSearch)._t = setTimeout(
      () => setRelSearchDebounced(v),
      300,
    );
  }

  const selectClass = cn(
    'rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-input)] px-2 py-1.5 text-[var(--text-sm)] text-[var(--color-text)]',
    'focus:outline-none focus:border-[var(--color-primary)] focus:ring-1 focus:ring-[var(--color-primary)]',
  );

  return (
    <div className="mt-6 border-t border-[var(--color-border)] pt-5" data-testid="relations-section">
      <h3 className="mb-3 flex items-center gap-2 text-[var(--text-sm)] font-semibold text-[var(--color-text)]">
        <Link2 className="h-4 w-4" />
        Relations
      </h3>
      {relations.length > 0 && (
        <div className="mb-4 space-y-1.5">
          {relations.map((rel) => (
            <div
              key={rel.id}
              data-testid={`relation-row-${rel.id}`}
              className="flex items-center gap-2 rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-2 text-[var(--text-sm)]"
            >
              <span className="shrink-0 rounded-full bg-[var(--color-surface-hover)] px-2 py-0.5 text-[var(--text-xs)] capitalize text-[var(--color-text-muted)]">
                {rel.direction === 'incoming' ? '← ' : ''}
                {rel.kind.replace(/_/g, ' ')}
              </span>
              {/*
                A relation whose far side sits in a space this viewer cannot
                read arrives with every far field null. Showing the row is
                deliberate — an entity needs to know it is blocked — but the
                placeholder must stay free of anything that identifies the far
                entity, so there is no title, no key and no link to follow.
              */}
              {rel.far_readable ? (
                <>
                  <FarEntityLink rel={rel} />
                  {rel.far_status && (
                    <span className="shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                      {rel.far_status}
                    </span>
                  )}
                </>
              ) : (
                <span className="flex-1 truncate italic text-[var(--color-text-muted)]">
                  Restricted item
                </span>
              )}
              <button
                aria-label="Remove relation"
                onClick={() => deleteRelationMutation.mutate(rel.id)}
                className="ml-1 rounded p-0.5 text-[var(--color-text-muted)] transition-colors hover:text-[var(--color-danger)]"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </div>
          ))}
        </div>
      )}
      <div className="flex gap-2">
        <select
          aria-label="Relation kind"
          value={relKind}
          onChange={(e) => setRelKind(e.target.value)}
          className={selectClass}
        >
          {RELATION_KINDS.map((k) => (
            <option key={k.value} value={k.value}>
              {k.label}
            </option>
          ))}
        </select>
        {targetChoices.length > 1 && (
          <select
            aria-label="Relation target type"
            value={targetType}
            onChange={(e) => setTargetType(e.target.value as 'project_item' | 'page')}
            className={selectClass}
          >
            {targetChoices.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </select>
        )}
        {targetType === 'page' ? (
          <PageRefField
            className="flex-1"
            orgId={orgId}
            testId="relation-page-ref"
            onSelect={(pageSel) =>
              createRelationMutation.mutate({
                to_id: pageSel.page_id,
                to_type: 'page',
                kind: relKind,
              })
            }
          />
        ) : (
          <div className="relative flex-1">
            <Input
              placeholder="Search items…"
              value={relSearch}
              onChange={(e) => handleRelSearchChange(e.target.value)}
            />
            {searchResults.length > 0 && relSearch && (
              <div className="absolute left-0 top-full z-50 mt-1 w-full rounded-[var(--radius-lg)] border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-md)]">
                {searchResults
                  .filter((r) => r.id !== entityId)
                  .slice(0, 8)
                  .map((r) => (
                    <button
                      key={r.id}
                      type="button"
                      className="flex w-full items-center gap-2 px-3 py-2 text-left text-[var(--text-sm)] text-[var(--color-text)] hover:bg-[var(--color-surface-hover)]"
                      onClick={async () => {
                        await createRelationMutation.mutateAsync({
                          to_id: r.id,
                          to_type: 'project_item',
                          kind: relKind,
                        });
                        setRelSearch('');
                        setRelSearchDebounced('');
                      }}
                    >
                      <span className="truncate">{r.title}</span>
                      <span className="ml-auto shrink-0 text-[var(--text-xs)] text-[var(--color-text-muted)]">
                        {r.status}
                      </span>
                    </button>
                  ))}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

/** The route each far entity type is viewed at. The module segments are the
 *  same ones TagPage and the shell use for these entities. */
function farEntityPath(rel: Relation): string | null {
  if (!rel.far_id || !rel.far_space_id || !rel.far_type) return null;
  switch (rel.far_type) {
    case 'page':
      return `/codex/${rel.far_space_id}/pages/${rel.far_id}`;
    case 'ticket':
      return `/beacon/${rel.far_space_id}/tickets/${rel.far_id}`;
    case 'project_item':
      return `/vector/${rel.far_space_id}/backlog/${rel.far_id}`;
    default:
      return null;
  }
}

function FarEntityLink({ rel }: { rel: Relation }) {
  const path = farEntityPath(rel);
  if (!path) {
    return <span className="flex-1 truncate text-[var(--color-text)]">{rel.far_title}</span>;
  }
  return (
    <Link
      to={path}
      data-testid={`relation-far-link-${rel.id}`}
      className="flex-1 truncate text-[var(--color-text)] hover:text-[var(--color-primary)] hover:underline"
    >
      {rel.far_title}
    </Link>
  );
}
