import { useMemo, useState } from 'react';
import { Plus, X } from 'lucide-react';
import { Badge } from '../ui/badge';
import { Button } from '../ui/button';
import { Input } from '../ui/input';
import { Field, FieldHint, FieldLabel } from '../ui/field';
import { SegmentedControl } from '../ui/segmented';
import { TypeFilter } from '../TypeFilter';
import { PersonTeamPicker, type PickedSubject } from '../PersonTeamPicker';
import { PRIORITY_LABEL, normalizePriority } from '../priority';
import { MODULES } from '../../shell/modules';
import { cn } from '../../lib/utils';
import { useItemTypes, useSpaces, useSprints } from '../../lib/api';
import {
  ASSIGNEE_ME,
  ASSIGNEE_UNASSIGNED,
  QUERY_LIMITS,
  VIEW_MODULES,
  VIEW_PRIORITIES,
  VIEW_SORT_FIELDS,
  hasModule,
  pruneVectorOnlyFields,
  vectorOnlyFieldsAllowed,
  vectorOnlyFieldsReason,
  type QueryDoc,
  type QueryFilter,
  type ViewModule,
  type ViewPriority,
  type ViewSortDir,
  type ViewSortField,
} from '../../lib/views/query';

/**
 * The structured filter builder (P4, ADR-0009).
 *
 * # There is no query box, and that is the product decision
 *
 * Every control here offers a closed vocabulary from `lib/views/query`. A
 * free-text query field would be a query LANGUAGE — it would need an operator
 * grammar, it would put column names in front of users, and every saved
 * document would then have to be parsed rather than read. `QueryDoc` is a
 * record, not a language, and this component is the reason it can stay one.
 * The `text` control below is a filter field (a literal substring matched
 * against the title), not a query box; do not grow it into one.
 *
 * Controlled: it holds no copy of the document. The one exception is the
 * labels of picked people, which the wire does not carry back — see below.
 */

const SORT_FIELD_LABEL: Record<ViewSortField, string> = {
  updated_at: 'Last updated',
  created_at: 'Created',
  due_at: 'Due date',
  resolved_at: 'Resolved',
  priority: 'Priority',
  title: 'Title',
};

const SORT_DIR_OPTIONS: { value: ViewSortDir; label: string }[] = [
  { value: 'desc', label: 'Descending' },
  { value: 'asc', label: 'Ascending' },
];

const selectClass = cn(
  'h-9 rounded-[var(--radius-lg)] border border-[var(--color-border)]',
  'bg-[var(--color-input)] px-3 text-[var(--text-sm)] text-[var(--color-text)]',
  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
);

/**
 * The optional multi-value fields. `modules` is deliberately not one of them:
 * it is required, it is never cleared, and its toggle carries the vector-only
 * prune that no other field's does.
 */
type ListField = 'space_ids' | 'statuses' | 'priorities' | 'assignees' | 'kinds' | 'sprint_ids';

/**
 * Sets a list field, or removes it entirely when the selection is empty. An
 * absent field and an empty one both mean "not filtered", and omitting it keeps
 * the stored document the smallest true statement of what the view asks for.
 */
function withList(filter: QueryFilter, key: ListField, values: string[]): QueryFilter {
  const next = { ...filter };
  if (values.length === 0) delete next[key];
  else (next as Record<string, unknown>)[key] = values;
  return next;
}

function toggled(list: readonly string[] | undefined, value: string): string[] {
  const current = list ?? [];
  return current.includes(value) ? current.filter((v) => v !== value) : [...current, value];
}

interface QueryFilterBuilderProps {
  orgId: string;
  value: QueryDoc;
  onChange: (next: QueryDoc) => void;
}

export function QueryFilterBuilder({ orgId, value, onChange }: QueryFilterBuilderProps) {
  const filter = value.filter;
  const modules = filter.modules;
  const vectorOnlyOK = vectorOnlyFieldsAllowed(modules);
  const vectorOnlyWhy = vectorOnlyFieldsReason(modules);

  const spacesQuery = useSpaces(orgId);
  const itemTypesQuery = useItemTypes(orgId);

  // A person's display name is not on the wire in a filter document — only the
  // id is — so the labels of people picked in THIS session are remembered here.
  // Reopening a saved view shows an unlabelled id rather than a wrong name.
  const [personLabels, setPersonLabels] = useState<Record<string, string>>({});
  const [statusDraft, setStatusDraft] = useState('');

  function setFilter(next: QueryFilter) {
    onChange({ ...value, filter: next });
  }

  function setList(key: ListField, values: string[]) {
    setFilter(withList(filter, key, values));
  }

  /**
   * Module toggle. The vector-only prune happens HERE, where the person can see
   * their type and sprint selection clear as a consequence of the click they
   * just made — never on the way to the wire, which would change what the view
   * means after they had read it on screen.
   */
  function toggleModule(m: ViewModule) {
    const next = hasModule(modules, m)
      ? modules.filter((x) => x !== m)
      : VIEW_MODULES.filter((x) => x === m || hasModule(modules, x));
    setFilter(pruneVectorOnlyFields({ ...filter, modules: [...next] }));
  }

  // Codex is absent from VIEW_MODULES, so its spaces can never be searched by a
  // saved view and are not offered.
  const spaceOptions = useMemo(() => {
    const rows = (spacesQuery.data ?? []).filter(
      (s) => s.readable !== false && (s.type === 'beacon' || s.type === 'vector'),
    );
    return rows
      .map((s) => ({ slug: s.id, name: s.key ? `${s.key} · ${s.name}` : s.name }))
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [spacesQuery.data]);

  const selectedSpaces = useMemo(
    () =>
      (spacesQuery.data ?? []).filter((s) => (filter.space_ids ?? []).includes(s.id)),
    [spacesQuery.data, filter.space_ids],
  );

  const kindOptions = useMemo(
    () =>
      (itemTypesQuery.data ?? [])
        .filter((t) => !t.archived_at)
        .map((t) => ({ slug: t.slug, name: t.name })),
    [itemTypesQuery.data],
  );

  const assignees = filter.assignees ?? [];
  const pickedPeople = assignees.filter((a) => a !== ASSIGNEE_ME && a !== ASSIGNEE_UNASSIGNED);

  function addPerson(subject: PickedSubject | null) {
    if (!subject || subject.kind !== 'user') return;
    // The literal token is what a viewer-relative filter stores. Substituting
    // the current user's id here would freeze a shared view to one person.
    if (assignees.includes(subject.id)) return;
    setPersonLabels((prev) => ({ ...prev, [subject.id]: subject.label }));
    setList('assignees', [...assignees, subject.id]);
  }

  function addStatus() {
    const status = statusDraft.trim();
    if (!status) return;
    const current = filter.statuses ?? [];
    if (!current.includes(status)) setList('statuses', [...current, status]);
    setStatusDraft('');
  }

  return (
    <div data-testid="query-filter-builder" className="space-y-1">
      <Field>
        <FieldLabel id="view-modules-label">Modules</FieldLabel>
        <TypeFilter
          label="Modules this view searches"
          testId="view-modules"
          options={VIEW_MODULES.map((m) => ({ slug: m, name: MODULES[m].name }))}
          selected={new Set<string>(modules)}
          onToggle={(slug) => toggleModule(slug as ViewModule)}
        />
        <FieldHint>
          {modules.length === 0
            ? 'Choose at least one module for this view to search.'
            : 'A view that names both modules returns one merged list.'}
        </FieldHint>
      </Field>

      <Field>
        <FieldLabel>Spaces</FieldLabel>
        {spaceOptions.length > 0 ? (
          <TypeFilter
            label="Spaces this view searches"
            testId="view-spaces"
            options={spaceOptions}
            selected={new Set(filter.space_ids ?? [])}
            onToggle={(id) => setList('space_ids', toggled(filter.space_ids, id))}
          />
        ) : (
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            No Beacon or Vector spaces are available to you yet.
          </p>
        )}
        <FieldHint>
          {(filter.space_ids?.length ?? 0) === 0
            ? 'No selection searches every space you can read — including spaces added later.'
            : `Searching ${filter.space_ids!.length} space${filter.space_ids!.length === 1 ? '' : 's'}. At most ${QUERY_LIMITS.space_ids}.`}
        </FieldHint>
      </Field>

      <Field>
        <FieldLabel htmlFor="view-status-input">Statuses</FieldLabel>
        <div className="flex flex-wrap items-center gap-2">
          {(filter.statuses ?? []).map((s) => (
            <Badge key={s} variant="default" className="gap-1" data-testid={`view-status-${s}`}>
              {s}
              <button
                type="button"
                aria-label={`Remove status ${s}`}
                onClick={() => setList('statuses', toggled(filter.statuses, s))}
                className="rounded-full p-0.5 hover:bg-[var(--color-surface-hover)]"
              >
                <X className="h-3 w-3" />
              </button>
            </Badge>
          ))}
        </div>
        <div className="mt-1.5 flex items-center gap-2">
          <Input
            id="view-status-input"
            data-testid="view-status-input"
            className="max-w-xs"
            placeholder="e.g. in_progress"
            value={statusDraft}
            onChange={(e) => setStatusDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                addStatus();
              }
            }}
          />
          <Button type="button" variant="outline" size="sm" onClick={addStatus} data-testid="view-status-add">
            <Plus className="mr-1 h-4 w-4" />
            Add
          </Button>
        </div>
        {/* Free text on purpose: workflow states are defined per space, so
            there is no org-wide list to offer. */}
        <FieldHint>Workflow states are defined per space, so statuses are typed rather than picked.</FieldHint>
      </Field>

      <Field>
        <FieldLabel>Priorities</FieldLabel>
        <TypeFilter
          label="Priorities"
          testId="view-priorities"
          options={VIEW_PRIORITIES.map((p) => ({ slug: p, name: PRIORITY_LABEL[normalizePriority(p)] }))}
          selected={new Set(filter.priorities ?? [])}
          onToggle={(p) =>
            setList('priorities', toggled(filter.priorities as string[] | undefined, p as ViewPriority))
          }
        />
      </Field>

      <Field>
        <FieldLabel>Assignees</FieldLabel>
        <div className="flex flex-wrap items-center gap-2">
          <TypeFilter
            label="Assignee shortcuts"
            testId="view-assignee-tokens"
            options={[
              { slug: ASSIGNEE_ME, name: 'Me' },
              { slug: ASSIGNEE_UNASSIGNED, name: 'Unassigned' },
            ]}
            selected={new Set(assignees)}
            onToggle={(token) => setList('assignees', toggled(assignees, token))}
          />
          {pickedPeople.map((id) => (
            <Badge key={id} variant="default" className="gap-1" data-testid={`view-assignee-${id}`}>
              {personLabels[id] ?? id.slice(0, 8)}
              <button
                type="button"
                aria-label="Remove assignee"
                onClick={() => setList('assignees', toggled(assignees, id))}
                className="rounded-full p-0.5 hover:bg-[var(--color-surface-hover)]"
              >
                <X className="h-3 w-3" />
              </button>
            </Badge>
          ))}
        </div>
        <div className="mt-1.5">
          <PersonTeamPicker
            orgId={orgId}
            subjects="user"
            value={null}
            onChange={addPerson}
            placeholder="Add a person…"
            testId="view-assignee-picker"
          />
        </div>
        <FieldHint>
          “Me” is stored as the literal token and resolved per viewer, so a shared view means each
          reader’s own work rather than yours.
        </FieldHint>
      </Field>

      {/* Vector-only fields. The rule has exactly one answer in the frontend
          (vectorOnlyFieldsAllowed) and this is the only place the UI asks it:
          naming a type or a sprint alongside Beacon is a 422 on the whole
          document, not an empty Beacon half. */}
      <Field>
        <FieldLabel>Types</FieldLabel>
        {kindOptions.length > 0 ? (
          <TypeFilter
            label="Item types"
            testId="view-kinds"
            options={kindOptions}
            selected={new Set(filter.kinds ?? [])}
            onToggle={(k) => setList('kinds', toggled(filter.kinds, k))}
            disabled={!vectorOnlyOK}
          />
        ) : (
          <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
            No item types are defined in this organisation.
          </p>
        )}
        {vectorOnlyWhy && <FieldHint data-testid="view-kinds-reason">{vectorOnlyWhy}</FieldHint>}
      </Field>

      <Field>
        <FieldLabel>Sprints</FieldLabel>
        {vectorOnlyWhy ? (
          <FieldHint data-testid="view-sprints-reason">{vectorOnlyWhy}</FieldHint>
        ) : selectedSpaces.length === 0 ? (
          <FieldHint>Choose one or more spaces above to filter by sprint.</FieldHint>
        ) : (
          <div className="space-y-2">
            {selectedSpaces.map((s) => (
              <SpaceSprintChips
                key={s.id}
                spaceId={s.id}
                spaceName={s.name}
                selected={new Set(filter.sprint_ids ?? [])}
                onToggle={(id) => setList('sprint_ids', toggled(filter.sprint_ids, id))}
              />
            ))}
          </div>
        )}
      </Field>

      <Field>
        <FieldLabel htmlFor="view-text">Text</FieldLabel>
        <Input
          id="view-text"
          data-testid="view-text"
          className="max-w-md"
          maxLength={QUERY_LIMITS.text}
          placeholder="Words in the title"
          value={filter.text ?? ''}
          onChange={(e) => {
            const next = { ...filter };
            if (e.target.value) next.text = e.target.value;
            else delete next.text;
            setFilter(next);
          }}
        />
        <FieldHint>
          A literal substring matched against the title — not a pattern and not a query language.
          Up to {QUERY_LIMITS.text} characters.
        </FieldHint>
      </Field>

      <Field>
        <FieldLabel htmlFor="view-sort-field">Sort</FieldLabel>
        <div className="flex flex-wrap items-center gap-3">
          <select
            id="view-sort-field"
            data-testid="view-sort-field"
            className={selectClass}
            value={value.sort.field}
            onChange={(e) =>
              onChange({ ...value, sort: { ...value.sort, field: e.target.value as ViewSortField } })
            }
          >
            {VIEW_SORT_FIELDS.map((f) => (
              <option key={f} value={f}>
                {SORT_FIELD_LABEL[f]}
              </option>
            ))}
          </select>
          <SegmentedControl
            aria-label="Sort direction"
            testId="view-sort-dir"
            fullWidth={false}
            options={SORT_DIR_OPTIONS}
            value={value.sort.dir}
            onChange={(dir) => onChange({ ...value, sort: { ...value.sort, dir } })}
          />
        </div>
        {/* Status is absent from the list on purpose: it is free text with no
            total order, so sorting by it would order alphabetically. */}
        <FieldHint>One field, one direction — the results cursor encodes a single key.</FieldHint>
      </Field>
    </div>
  );
}

/**
 * One space's sprints. Sprints are space-scoped with no org-wide listing, so
 * the control is composed per selected space — each instance owns its own
 * query, and unselecting a space unmounts its query rather than leaving it
 * cached behind a filter nobody can see.
 */
function SpaceSprintChips({
  spaceId,
  spaceName,
  selected,
  onToggle,
}: {
  spaceId: string;
  spaceName: string;
  selected: Set<string>;
  onToggle: (sprintId: string) => void;
}) {
  const sprints = useSprints(spaceId);
  const options = (sprints.data ?? []).map((s) => ({ slug: s.id, name: s.name }));
  return (
    <div>
      <p className="pb-1 text-[11px] uppercase tracking-wider text-[var(--color-text-muted)]">
        {spaceName}
      </p>
      {options.length > 0 ? (
        <TypeFilter
          label={`Sprints in ${spaceName}`}
          testId={`view-sprints-${spaceId}`}
          options={options}
          selected={selected}
          onToggle={onToggle}
        />
      ) : (
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">No sprints here yet.</p>
      )}
    </div>
  );
}
