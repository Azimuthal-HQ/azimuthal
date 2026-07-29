import { useEffect, useMemo, useState } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';
import { AlertCircle, ArrowLeft } from 'lucide-react';
import { Button } from '../../components/ui/button';
import { Input } from '../../components/ui/input';
import { Field, FieldHint, FieldLabel } from '../../components/ui/field';
import { SegmentedControl } from '../../components/ui/segmented';
import { PersonTeamPicker, type PickedSubject } from '../../components/PersonTeamPicker';
import { QueryFilterBuilder } from '../../components/views/QueryFilterBuilder';
import { ViewResultList } from '../../components/views/ViewResultList';
import { cn } from '../../lib/utils';
import { useAuth } from '../../lib/auth';
import {
  friendlyErrorMessage,
  useCreateView,
  usePreviewResults,
  useSavedView,
  useUpdateView,
  type SavedView,
  type ViewRequest,
  type ViewVisibility,
} from '../../lib/api';
import { readViewDraft } from '../../lib/views/draft';
import {
  QUERY_LIMITS,
  emptyQueryDoc,
  validateQueryDoc,
  type QueryDoc,
} from '../../lib/views/query';

/**
 * /views/new and /views/{id}/edit — the view builder (P4, ADR-0009).
 *
 * The filter half lives in `QueryFilterBuilder`; this page owns the things a
 * view has besides its query — its name, its description, and who it is shared
 * with — plus the save, and the live preview.
 *
 * The preview runs through `usePreviewResults`, which is the SAME resolution
 * path a saved view's results take. That is deliberate: a builder that
 * approximated the result locally would agree with the server right up until
 * the moment it mattered.
 */

const VISIBILITY_OPTIONS: { value: ViewVisibility; label: string }[] = [
  { value: 'private', label: 'Private' },
  { value: 'team', label: 'Team' },
  { value: 'org', label: 'Organisation' },
];

const VISIBILITY_HINT: Record<ViewVisibility, string> = {
  private: 'Only you can open this view.',
  team: 'Everyone in the chosen team can open it. Each of them sees it resolved against their own access.',
  org: 'Everyone in the organisation can open it. Each of them sees it resolved against their own access.',
};

interface BuilderForm {
  name: string;
  description: string;
  visibility: ViewVisibility;
  teamId: string | null;
  teamLabel?: string;
  query: QueryDoc;
}

function formFromView(view: SavedView): BuilderForm {
  return {
    name: view.name,
    description: view.description,
    visibility: view.visibility,
    teamId: view.visibility_team_id,
    teamLabel: view.team_name,
    query: view.query,
  };
}

export function ViewBuilderPage() {
  const { viewId } = useParams<{ viewId: string }>();
  const isEdit = Boolean(viewId);
  const location = useLocation();
  const navigate = useNavigate();
  const { user } = useAuth();
  const orgId = user?.orgId ?? '';

  const viewQuery = useSavedView(orgId, viewId ?? '', { enabled: !!orgId && isEdit });
  const createView = useCreateView(orgId);
  const updateView = useUpdateView(orgId);

  /**
   * The prefill from a list page's "Save as view". Location state is
   * user-reachable, so it is parsed rather than trusted — an unrecognisable
   * one yields null and the builder opens on the broad default.
   */
  const initial: BuilderForm | null = useMemo(() => {
    if (isEdit) return viewQuery.data ? formFromView(viewQuery.data) : null;
    const draft = readViewDraft(location.state);
    return {
      name: draft?.name ?? '',
      description: draft?.description ?? '',
      visibility: 'private',
      teamId: null,
      query: draft?.query ?? emptyQueryDoc(),
    };
  }, [isEdit, viewQuery.data, location.state]);

  // Held only once something has been edited, so the loaded view seeds the
  // form without an effect that would fight the user's own typing.
  const [edited, setEdited] = useState<BuilderForm | null>(null);
  const form = edited ?? initial;

  function update(patch: Partial<BuilderForm>) {
    if (!form) return;
    setEdited({ ...form, ...patch });
  }

  // --- The live preview ----------------------------------------------------

  const preview = usePreviewResults(orgId);
  const runPreview = preview.mutate;
  const queryDoc = form?.query;
  const queryJSON = useMemo(() => (queryDoc ? JSON.stringify(queryDoc) : ''), [queryDoc]);
  const queryProblem = queryDoc ? validateQueryDoc(queryDoc) : null;

  useEffect(() => {
    if (!orgId || !queryJSON || queryProblem) return;
    // Debounced: the document changes on every keystroke, and the preview is a
    // real resolution against the database rather than a local filter.
    const timer = setTimeout(() => runPreview({ query: JSON.parse(queryJSON) as QueryDoc }), 350);
    return () => clearTimeout(timer);
  }, [orgId, queryJSON, queryProblem, runPreview]);

  // --- Saving --------------------------------------------------------------

  const trimmedName = form?.name.trim() ?? '';
  const teamMissing = form?.visibility === 'team' && !form.teamId;
  const saveProblem =
    !trimmedName
      ? 'Give this view a name.'
      : teamMissing
        ? 'Choose the team this view is shared with.'
        : queryProblem;

  const saveError = isEdit ? updateView.error : createView.error;
  const saving = createView.isPending || updateView.isPending;

  async function save() {
    if (!form || saveProblem) return;
    const req: ViewRequest = {
      name: trimmedName,
      description: form.description.trim(),
      query: form.query,
      visibility: form.visibility,
      // Sent as null for anything other than team visibility, so a view moved
      // from team to private does not keep a stale audience on the row.
      visibility_team_id: form.visibility === 'team' ? form.teamId : null,
    };
    try {
      if (isEdit && viewId) {
        await updateView.mutateAsync({ viewId, req });
        navigate(`/views/${viewId}`);
      } else {
        const created = await createView.mutateAsync(req);
        navigate(`/views/${created.id}`);
      }
    } catch {
      // Surfaced below through friendlyErrorMessage.
    }
  }

  // --- Render --------------------------------------------------------------

  if (isEdit && viewQuery.error) {
    return (
      <div className="space-y-4">
        <BackLink />
        <div
          data-testid="view-error"
          className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
        >
          <AlertCircle className="h-5 w-5 shrink-0 text-[var(--color-danger)]" />
          <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
            {friendlyErrorMessage(viewQuery.error, 'This view is unavailable right now.')}
          </p>
        </div>
      </div>
    );
  }

  if (!form) {
    return (
      <div className="space-y-4">
        <BackLink />
        <div className="flex h-32 items-center justify-center text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Loading this view…
        </div>
      </div>
    );
  }

  const teamSubject: PickedSubject | null = form.teamId
    ? { kind: 'team', id: form.teamId, label: form.teamLabel ?? 'Selected team' }
    : null;

  return (
    <div className="space-y-5">
      <BackLink />

      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-[var(--text-lg)] font-semibold tracking-[-.01em] text-[var(--color-text)]">
          {isEdit ? 'Edit view' : 'New view'}
        </h1>
        <div className="flex items-center gap-2">
          <Button asChild variant="outline">
            <Link to={isEdit && viewId ? `/views/${viewId}` : '/views'}>Cancel</Link>
          </Button>
          <Button onClick={save} disabled={saving || Boolean(saveProblem)} data-testid="save-view">
            {saving ? 'Saving…' : isEdit ? 'Save changes' : 'Create view'}
          </Button>
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        {/* Definition */}
        <div className="space-y-4">
          <section className="rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
            <Field>
              <FieldLabel htmlFor="view-name">Name</FieldLabel>
              <Input
                id="view-name"
                data-testid="view-name"
                maxLength={QUERY_LIMITS.name}
                placeholder="e.g. My open work across Support and Platform"
                value={form.name}
                onChange={(e) => update({ name: e.target.value })}
              />
            </Field>

            <Field>
              <FieldLabel htmlFor="view-description" optional>
                Description
              </FieldLabel>
              <textarea
                id="view-description"
                data-testid="view-description"
                rows={2}
                maxLength={QUERY_LIMITS.description}
                placeholder="What this view is for, in a sentence"
                value={form.description}
                onChange={(e) => update({ description: e.target.value })}
                className={cn(
                  'flex w-full resize-y rounded-[var(--radius-lg)] border border-[var(--color-border)]',
                  'bg-[var(--color-input)] px-3 py-2 text-[var(--text-sm)] text-[var(--color-text)]',
                  'placeholder:text-[var(--color-text-muted)] transition-colors',
                  'focus-visible:outline-none focus-visible:border-[var(--color-primary)] focus-visible:ring-1 focus-visible:ring-[var(--color-primary)]',
                )}
              />
            </Field>

            <Field>
              <FieldLabel>Who can open it</FieldLabel>
              <SegmentedControl
                aria-label="Visibility"
                testId="view-visibility"
                options={VISIBILITY_OPTIONS}
                value={form.visibility}
                onChange={(visibility) => update({ visibility })}
              />
              <FieldHint>{VISIBILITY_HINT[form.visibility]}</FieldHint>
              {form.visibility === 'team' && (
                <div className="mt-2">
                  <PersonTeamPicker
                    orgId={orgId}
                    subjects="team"
                    testId="view-team-picker"
                    value={teamSubject}
                    onChange={(subject) =>
                      update({
                        teamId: subject?.kind === 'team' ? subject.id : null,
                        teamLabel: subject?.kind === 'team' ? subject.label : undefined,
                      })
                    }
                  />
                </div>
              )}
            </Field>
          </section>

          <section className="rounded-[var(--radius-xl)] border border-[var(--color-border)] bg-[var(--color-surface)] p-4">
            <QueryFilterBuilder
              orgId={orgId}
              value={form.query}
              onChange={(query) => update({ query })}
            />
          </section>

          {saveProblem && (
            <p data-testid="save-problem" className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
              {saveProblem}
            </p>
          )}

          {saveError && (
            <div
              data-testid="save-error"
              className="flex items-center gap-3 rounded-[var(--radius-lg)] border border-[var(--color-danger)] bg-[color-mix(in_srgb,var(--color-danger)_10%,transparent)] p-4"
            >
              <AlertCircle className="h-5 w-5 shrink-0 text-[var(--color-danger)]" />
              <p className="text-[var(--text-sm)] text-[var(--color-danger)]">
                {friendlyErrorMessage(
                  saveError,
                  isEdit ? 'The changes to this view were not saved.' : 'This view was not saved.',
                )}
              </p>
            </div>
          )}
        </div>

        {/* Live preview */}
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <h2 className="text-[var(--text-sm)] font-semibold text-[var(--color-text)]">Preview</h2>
            <span className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
              {preview.isPending ? 'Resolving…' : 'Resolved against your access'}
            </span>
          </div>

          {queryProblem ? (
            <p
              data-testid="preview-blocked"
              className="rounded-[var(--radius-lg)] border border-dashed border-[var(--color-border)] p-4 text-[var(--text-sm)] text-[var(--color-text-muted)]"
            >
              {queryProblem}
            </p>
          ) : (
            <>
              <ViewResultList
                testId="view-preview"
                page={preview.data}
                isLoading={preview.isPending}
                error={preview.error}
                errorFallback="The preview is unavailable right now."
                emptyTitle="Nothing matches yet"
                emptyDescription="No work in the spaces you can read matches these filters. Widen them, or save the view anyway — it is resolved fresh every time it is opened."
                meId={user?.id}
              />
              {preview.data?.has_more && (
                <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
                  The first page only — the saved view pages through the rest.
                </p>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function BackLink() {
  return (
    <Link
      to="/views"
      className="inline-flex items-center gap-1 text-[var(--text-sm)] text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
    >
      <ArrowLeft className="h-4 w-4" />
      All views
    </Link>
  );
}
