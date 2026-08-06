import { useState } from 'react';
import { Plus, ShieldAlert, Trash2, UserCheck, Zap } from 'lucide-react';
import { cn } from '../../../lib/utils';
import { PersonTeamPicker, type PickedSubject } from '../../../components/PersonTeamPicker';
import {
  friendlyErrorMessage,
  useCreateTransitionApprover,
  useCreateTransitionGuard,
  useCreateTransitionPostFunction,
  useDeleteTransitionApprover,
  useDeleteTransitionGuard,
  useDeleteTransitionPostFunction,
  useTransitionApprovers,
  useTransitionGuards,
  useTransitionPostFunctions,
  type WorkflowApprover,
  type WorkflowGuard,
  type WorkflowPostFunction,
} from '../../../lib/api';
import {
  GUARD_CAPABILITIES,
  GUARD_CAPABILITY_LABEL,
  GUARD_CLASSES,
  GUARD_CLASS_LABEL,
  GUARD_FIELD_KEYS,
  GUARD_FIELD_LABEL,
  GUARD_KINDS,
  GUARD_KIND_LABEL,
  GUARD_KIND_PARAM,
  POST_FIELD_KEYS,
  POST_FIELD_LABEL,
  POST_FUNCTION_KINDS,
  POST_FUNCTION_KIND_LABEL,
  type GuardCapability,
  type GuardClass,
  type GuardFieldKey,
  type GuardKind,
  type PostFieldKey,
  type PostFunctionKind,
} from '../../../lib/workflow/vocabulary';

/**
 * The ADR-0011 tier editor for one transition (W4).
 *
 * # The vocabulary IS the form
 *
 * Every input here is a picker over a closed list. There is no free-text
 * predicate field, no expression box, and no way to name a field the code does
 * not already know — ADR-0011 refuses scripting "at any tier, under any
 * framing, in any edition, now or later", and that refusal is only real if the
 * form cannot become a language by accident. Which parameter each kind takes
 * comes from GUARD_KIND_PARAM, mirroring migration 046's shape CHECK, so the
 * form cannot express a shape the server would refuse.
 *
 * # An untouched transition renders nothing
 *
 * A transition with no guards, no approvers and no post-functions shows none of
 * those sections — no empty table, no "0 rules" badge, no "unrestricted" label,
 * no dashed call-to-action box. That is the UI half of the guarantee
 * TestGate_UntouchedWorkflowIsUnaffected makes at the service layer: an
 * administrator who has configured nothing must see what they saw before
 * migration 046, and a page announcing an absence is not that.
 *
 * The single "Add a rule" control is the deliberate exception, and it is the
 * whole reason this surface exists — the phase's brief requires per-transition
 * editing, which cannot be delivered invisibly. See the PR body.
 */

interface TransitionRulesProps {
  orgId: string;
  workflowId: string;
  transitionId: string;
}

export function TransitionRules({ orgId, workflowId, transitionId }: TransitionRulesProps) {
  const guards = useTransitionGuards(orgId, workflowId, transitionId);
  const approvers = useTransitionApprovers(orgId, workflowId, transitionId);
  const postFns = useTransitionPostFunctions(orgId, workflowId, transitionId);

  const [adding, setAdding] = useState<'guard' | 'approver' | 'post_function' | null>(null);

  const guardRows = guards.data ?? [];
  const approverRows = approvers.data ?? [];
  const postFnRows = postFns.data ?? [];

  return (
    <div className="space-y-[var(--space-3)]" data-testid={`transition-rules-${transitionId}`}>
      {guardRows.length > 0 && (
        <RuleGroup icon={<ShieldAlert className="h-3.5 w-3.5" />} title="Conditions and validators">
          {guardRows.map((g) => (
            <GuardRow
              key={g.id}
              guard={g}
              orgId={orgId}
              workflowId={workflowId}
              transitionId={transitionId}
            />
          ))}
        </RuleGroup>
      )}

      {approverRows.length > 0 && (
        <RuleGroup icon={<UserCheck className="h-3.5 w-3.5" />} title="Approvers">
          {approverRows.map((a) => (
            <ApproverRow
              key={a.id}
              approver={a}
              orgId={orgId}
              workflowId={workflowId}
              transitionId={transitionId}
            />
          ))}
          <p className="pt-1 text-[var(--text-xs)] text-[var(--color-text-muted)]">
            Any one of these may decide. Until somebody does, the item stays where it is.
          </p>
        </RuleGroup>
      )}

      {postFnRows.length > 0 && (
        <RuleGroup icon={<Zap className="h-3.5 w-3.5" />} title="Actions on success">
          {postFnRows.map((p) => (
            <PostFunctionRow
              key={p.id}
              postFunction={p}
              orgId={orgId}
              workflowId={workflowId}
              transitionId={transitionId}
            />
          ))}
        </RuleGroup>
      )}

      {adding === null ? (
        <div className="flex flex-wrap gap-[var(--space-2)]">
          <AddButton testId={`add-guard-${transitionId}`} onClick={() => setAdding('guard')}>
            Add a rule
          </AddButton>
          <AddButton testId={`add-approver-${transitionId}`} onClick={() => setAdding('approver')}>
            Require approval
          </AddButton>
          <AddButton
            testId={`add-post-function-${transitionId}`}
            onClick={() => setAdding('post_function')}
          >
            Add an action
          </AddButton>
        </div>
      ) : (
        <div className="rounded-[var(--radius-md)] border border-[var(--color-border)] bg-[var(--color-surface-hover)] p-[var(--space-3)]">
          {adding === 'guard' && (
            <GuardForm
              orgId={orgId}
              workflowId={workflowId}
              transitionId={transitionId}
              nextPosition={guardRows.length}
              onDone={() => setAdding(null)}
            />
          )}
          {adding === 'approver' && (
            <ApproverForm
              orgId={orgId}
              workflowId={workflowId}
              transitionId={transitionId}
              existing={approverRows}
              onDone={() => setAdding(null)}
            />
          )}
          {adding === 'post_function' && (
            <PostFunctionForm
              orgId={orgId}
              workflowId={workflowId}
              transitionId={transitionId}
              nextPosition={postFnRows.length}
              onDone={() => setAdding(null)}
            />
          )}
        </div>
      )}
    </div>
  );
}

// ─── Presentation ─────────────────────────────────────────────────────────────

function RuleGroup({
  icon,
  title,
  children,
}: {
  icon: React.ReactNode;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 flex items-center gap-[var(--space-2)] text-[var(--text-xs)] font-medium uppercase tracking-wide text-[var(--color-text-muted)]">
        {icon}
        {title}
      </div>
      <div className="space-y-1">{children}</div>
    </div>
  );
}

function AddButton({
  testId,
  onClick,
  children,
}: {
  testId: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      data-testid={testId}
      onClick={onClick}
      className={cn(
        'inline-flex items-center gap-1 rounded-[var(--radius-md)] border border-dashed',
        'border-[var(--color-border)] px-[var(--space-2)] py-1 text-[var(--text-xs)]',
        'text-[var(--color-text-muted)] hover:border-[var(--color-primary)] hover:text-[var(--color-text)]',
      )}
    >
      <Plus className="h-3 w-3" />
      {children}
    </button>
  );
}

function RuleLine({
  children,
  testId,
  onDelete,
  deleting,
  degraded,
}: {
  children: React.ReactNode;
  testId: string;
  onDelete: () => void;
  deleting: boolean;
  degraded?: boolean;
}) {
  return (
    <div
      data-testid={testId}
      className={cn(
        'flex items-center gap-[var(--space-2)] rounded-[var(--radius-md)] px-[var(--space-2)] py-1',
        'text-[var(--text-sm)] hover:bg-[var(--color-surface-hover)]',
        degraded ? 'text-[var(--color-danger)]' : 'text-[var(--color-text)]',
      )}
    >
      <span className="flex-1">{children}</span>
      <button
        type="button"
        aria-label="Remove"
        data-testid={`${testId}-remove`}
        disabled={deleting}
        onClick={onDelete}
        className="shrink-0 rounded p-1 text-[var(--color-text-muted)] hover:bg-[var(--color-border)] hover:text-[var(--color-danger)] disabled:opacity-50"
      >
        <Trash2 className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

function FormError({ error, fallback }: { error: unknown; fallback: string }) {
  if (!error) return null;
  return (
    <p
      data-testid="rule-form-error"
      className="mt-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-danger)]"
    >
      {friendlyErrorMessage(error, fallback)}
    </p>
  );
}

const selectClass = cn(
  'h-8 rounded-[var(--radius-md)] border border-[var(--color-border)]',
  'bg-[var(--color-surface)] px-[var(--space-2)] text-[var(--text-sm)] text-[var(--color-text)]',
);

// ─── Guards ───────────────────────────────────────────────────────────────────

function guardSentence(guard: WorkflowGuard): string {
  switch (guard.kind) {
    case 'actor_is_assignee':
      return 'Only the assignee may make this transition';
    case 'actor_in_team':
      return guard.team_id
        ? 'Only members of a specific team'
        : // The DEGRADED state. migration 046 sets team_id NULL when the team is
          // deleted, so the restriction survives as unsatisfiable rather than
          // silently disappearing. It must read as needing attention — an admin
          // who saw "no restriction" here would be reading the opposite of the
          // truth — and it must be re-scoped rather than resubmitted, because
          // ValidateGuard refuses a null team on write.
          'Restricted to a team that no longer exists — remove this and add it again with a current team';
    case 'actor_has_capability':
      return guard.capability
        ? `Only people who can ${GUARD_CAPABILITY_LABEL[guard.capability].toLowerCase()}`
        : 'Names a capability that is missing from its configuration';
    case 'field_required':
      return guard.field_key
        ? `${GUARD_FIELD_LABEL[guard.field_key]} must be filled in`
        : 'Requires a field that is missing from its configuration';
    default:
      // A kind written by a newer build. The engine fails CLOSED on it, so the
      // transition is blocked entirely; saying so is more useful than rendering
      // a blank row.
      return `An unrecognised rule (${guard.kind}) — this build cannot evaluate it, so the transition is blocked`;
  }
}

function GuardRow({
  guard,
  orgId,
  workflowId,
  transitionId,
}: {
  guard: WorkflowGuard;
  orgId: string;
  workflowId: string;
  transitionId: string;
}) {
  const del = useDeleteTransitionGuard(orgId, workflowId, transitionId);
  const degraded = guard.kind === 'actor_in_team' && !guard.team_id;

  return (
    <RuleLine
      testId={`guard-${guard.id}`}
      onDelete={() => del.mutate(guard.id)}
      deleting={del.isPending}
      degraded={degraded}
    >
      <span className="mr-[var(--space-2)] rounded-full bg-[var(--color-surface-hover)] px-1.5 py-0.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
        {guard.guard_class === 'condition' ? 'hides' : 'refuses'}
      </span>
      {guardSentence(guard)}
    </RuleLine>
  );
}

function GuardForm({
  orgId,
  workflowId,
  transitionId,
  nextPosition,
  onDone,
}: {
  orgId: string;
  workflowId: string;
  transitionId: string;
  nextPosition: number;
  onDone: () => void;
}) {
  const [guardClass, setGuardClass] = useState<GuardClass>('validator');
  const [kind, setKind] = useState<GuardKind>('field_required');
  const [fieldKey, setFieldKey] = useState<GuardFieldKey>('assignee_id');
  const [capability, setCapability] = useState<GuardCapability>('transition_any_item');
  const [team, setTeam] = useState<PickedSubject | null>(null);

  const create = useCreateTransitionGuard(orgId, workflowId, transitionId);
  const param = GUARD_KIND_PARAM[kind];

  function submit() {
    create.mutate(
      {
        guard_class: guardClass,
        kind,
        position: nextPosition,
        // Exactly one parameter travels, chosen by kind. Sending a parameter a
        // kind does not take is a 400 from ValidateGuard and a shape-CHECK
        // violation one layer down.
        ...(param === 'field' ? { field_key: fieldKey } : {}),
        ...(param === 'capability' ? { capability } : {}),
        ...(param === 'team' && team ? { team_id: team.id } : {}),
      },
      { onSuccess: onDone },
    );
  }

  const incomplete = param === 'team' && !team;

  return (
    <div className="space-y-[var(--space-2)]" data-testid="guard-form">
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        <select
          aria-label="Rule effect"
          data-testid="guard-class"
          className={selectClass}
          value={guardClass}
          onChange={(e) => setGuardClass(e.target.value as GuardClass)}
        >
          {GUARD_CLASSES.map((c) => (
            <option key={c} value={c}>
              {GUARD_CLASS_LABEL[c]}
            </option>
          ))}
        </select>

        <select
          aria-label="Rule"
          data-testid="guard-kind"
          className={selectClass}
          value={kind}
          onChange={(e) => setKind(e.target.value as GuardKind)}
        >
          {GUARD_KINDS.map((k) => (
            <option key={k} value={k}>
              {GUARD_KIND_LABEL[k]}
            </option>
          ))}
        </select>

        {param === 'field' && (
          <select
            aria-label="Field"
            data-testid="guard-field"
            className={selectClass}
            value={fieldKey}
            onChange={(e) => setFieldKey(e.target.value as GuardFieldKey)}
          >
            {GUARD_FIELD_KEYS.map((f) => (
              <option key={f} value={f}>
                {GUARD_FIELD_LABEL[f]}
              </option>
            ))}
          </select>
        )}

        {param === 'capability' && (
          <select
            aria-label="Capability"
            data-testid="guard-capability"
            className={selectClass}
            value={capability}
            onChange={(e) => setCapability(e.target.value as GuardCapability)}
          >
            {/* A SUBSET of the capability model. Populating this from the full
                capability table would offer values ValidateGuard refuses, and
                migration 046 does not constrain this column, so nothing below
                the API would catch it. */}
            {GUARD_CAPABILITIES.map((c) => (
              <option key={c} value={c}>
                {GUARD_CAPABILITY_LABEL[c]}
              </option>
            ))}
          </select>
        )}

        {param === 'team' && (
          <PersonTeamPicker
            orgId={orgId}
            subjects="team"
            value={team}
            onChange={setTeam}
            testId="guard-team-picker"
          />
        )}
      </div>

      <FormActions
        onCancel={onDone}
        onSubmit={submit}
        pending={create.isPending}
        disabled={incomplete}
        submitTestId="guard-submit"
      />
      <FormError error={create.error} fallback="The rule could not be added." />
    </div>
  );
}

// ─── Approvers ────────────────────────────────────────────────────────────────

function ApproverRow({
  approver,
  orgId,
  workflowId,
  transitionId,
}: {
  approver: WorkflowApprover;
  orgId: string;
  workflowId: string;
  transitionId: string;
}) {
  const del = useDeleteTransitionApprover(orgId, workflowId, transitionId);

  return (
    <RuleLine
      testId={`approver-${approver.id}`}
      onDelete={() => del.mutate(approver.id)}
      deleting={del.isPending}
      degraded={approver.subject_missing}
    >
      <span className="mr-[var(--space-2)] rounded-full bg-[var(--color-surface-hover)] px-1.5 py-0.5 text-[var(--text-xs)] text-[var(--color-text-muted)]">
        {approver.subject_type}
      </span>
      {/* subject_name is resolved at read time and is EMPTY when the user or
          team has been deleted. Rendering it alone would show a blank row, so
          the id is the fallback and the state is named — the same pair the
          grants panel renders for a deleted grant subject. */}
      {approver.subject_name || approver.subject_id}
      {approver.subject_missing && (
        <span className="ml-[var(--space-2)] text-[var(--text-xs)]">(no longer in this org)</span>
      )}
    </RuleLine>
  );
}

function ApproverForm({
  orgId,
  workflowId,
  transitionId,
  existing,
  onDone,
}: {
  orgId: string;
  workflowId: string;
  transitionId: string;
  existing: WorkflowApprover[];
  onDone: () => void;
}) {
  const [subject, setSubject] = useState<PickedSubject | null>(null);
  const create = useCreateTransitionApprover(orgId, workflowId, transitionId);

  const already =
    subject !== null && existing.some((a) => a.subject_id === subject.id);

  return (
    <div className="space-y-[var(--space-2)]" data-testid="approver-form">
      {/* PersonTeamPicker is single-select and stops rendering its input once a
          value is held, so an approver LIST drives it with value={null} and
          appends on change — the pattern QueryFilterBuilder established. It is
          reused rather than forked: shared-surfaces.md §1 calls a second picker
          a defect.

          `subjects="both"` and no kind toggle, deliberately: the subject's own
          kind comes back on the selection, so there is no toggle to contradict
          it. Only 'user' and 'team' are offered — ADR-0011's third value,
          "role", is not representable and CreateApprover 400s on it. */}
      <PersonTeamPicker
        orgId={orgId}
        subjects="both"
        value={subject}
        onChange={setSubject}
        placeholder="Search people or teams…"
        testId="approver-picker"
      />

      {already && (
        <p className="text-[var(--text-sm)] text-[var(--color-text-muted)]">
          Already an approver for this transition.
        </p>
      )}

      <FormActions
        onCancel={onDone}
        onSubmit={() =>
          subject &&
          create.mutate(
            { subject_type: subject.kind, subject_id: subject.id },
            { onSuccess: onDone },
          )
        }
        pending={create.isPending}
        disabled={!subject || already}
        submitTestId="approver-submit"
      />
      <FormError error={create.error} fallback="The approver could not be added." />
    </div>
  );
}

// ─── Post-functions ───────────────────────────────────────────────────────────

function postFunctionSentence(
  postFunction: WorkflowPostFunction,
  teamlessName: (id: string) => string,
): string {
  switch (postFunction.kind) {
    case 'assign_to':
      return postFunction.assignee_user_id
        ? `Assign to ${teamlessName(postFunction.assignee_user_id)}`
        : 'Assign to somebody — the person is missing from its configuration';
    case 'set_field':
      if (!postFunction.field_key) return 'Set a field that is missing from its configuration';
      return `Set ${POST_FIELD_LABEL[postFunction.field_key].toLowerCase()} to “${
        postFunction.field_value ?? ''
      }”`;
    default:
      // PlanPostFunctions ABORTS on an unknown kind rather than skipping it, so
      // the transition fails entirely. Say that, rather than showing a row that
      // looks inert.
      return `An unrecognised action (${postFunction.kind}) — this build cannot perform it, so the transition will fail`;
  }
}

function PostFunctionRow({
  postFunction,
  orgId,
  workflowId,
  transitionId,
}: {
  postFunction: WorkflowPostFunction;
  orgId: string;
  workflowId: string;
  transitionId: string;
}) {
  const del = useDeleteTransitionPostFunction(orgId, workflowId, transitionId);

  return (
    <RuleLine
      testId={`post-function-${postFunction.id}`}
      onDelete={() => del.mutate(postFunction.id)}
      deleting={del.isPending}
    >
      {postFunctionSentence(postFunction, (id) => id.slice(0, 8))}
    </RuleLine>
  );
}

function PostFunctionForm({
  orgId,
  workflowId,
  transitionId,
  nextPosition,
  onDone,
}: {
  orgId: string;
  workflowId: string;
  transitionId: string;
  nextPosition: number;
  onDone: () => void;
}) {
  const [kind, setKind] = useState<PostFunctionKind>('assign_to');
  const [assignee, setAssignee] = useState<PickedSubject | null>(null);
  const [fieldKey, setFieldKey] = useState<PostFieldKey>('tags');
  const [fieldValue, setFieldValue] = useState('');
  const create = useCreateTransitionPostFunction(orgId, workflowId, transitionId);

  function submit() {
    create.mutate(
      kind === 'assign_to'
        ? { kind, position: nextPosition, assignee_user_id: assignee?.id }
        : { kind, position: nextPosition, field_key: fieldKey, field_value: fieldValue },
      { onSuccess: onDone },
    );
  }

  return (
    <div className="space-y-[var(--space-2)]" data-testid="post-function-form">
      <div className="flex flex-wrap items-center gap-[var(--space-2)]">
        <select
          aria-label="Action"
          data-testid="post-function-kind"
          className={selectClass}
          value={kind}
          onChange={(e) => setKind(e.target.value as PostFunctionKind)}
        >
          {POST_FUNCTION_KINDS.map((k) => (
            <option key={k} value={k}>
              {POST_FUNCTION_KIND_LABEL[k]}
            </option>
          ))}
        </select>

        {kind === 'assign_to' && (
          // Users only. `assignee_id` is a FK to users on both tables, so
          // assigning to a TEAM is not representable — ADR-0011 names it and
          // this build refuses it by having no column for it.
          <PersonTeamPicker
            orgId={orgId}
            subjects="user"
            value={assignee}
            onChange={setAssignee}
            testId="post-function-assignee"
          />
        )}

        {kind === 'set_field' && (
          <>
            <select
              aria-label="Field to set"
              data-testid="post-function-field"
              className={selectClass}
              value={fieldKey}
              onChange={(e) => setFieldKey(e.target.value as PostFieldKey)}
            >
              {/* NARROWER than the guard field list on purpose: guards read four
                  fields, post-functions write two. `description` is readable and
                  not writable, and `assignee_id` belongs to the assign action. */}
              {POST_FIELD_KEYS.map((f) => (
                <option key={f} value={f}>
                  {POST_FIELD_LABEL[f]}
                </option>
              ))}
            </select>
            <input
              type="text"
              aria-label="Value"
              data-testid="post-function-value"
              className={cn(selectClass, 'w-56')}
              value={fieldValue}
              onChange={(e) => setFieldValue(e.target.value)}
              placeholder={
                fieldKey === 'tags' ? 'tag-one, tag-two' : '2026-12-31T00:00:00Z'
              }
            />
          </>
        )}
      </div>

      {kind === 'set_field' && (
        <p className="text-[var(--text-xs)] text-[var(--color-text-muted)]">
          {fieldKey === 'tags'
            ? 'Comma separated. This REPLACES the item’s tags rather than adding to them.'
            : 'RFC3339, for example 2026-12-31T00:00:00Z. Leave empty to clear the due date.'}
        </p>
      )}

      <FormActions
        onCancel={onDone}
        onSubmit={submit}
        pending={create.isPending}
        disabled={kind === 'assign_to' && !assignee}
        submitTestId="post-function-submit"
      />
      {/* The value is parsed at WRITE time, so a malformed date is refused here
          rather than at transition time — which is the difference between an
          admin fixing their own typo and a user hitting a broken transition
          weeks later. */}
      <FormError error={create.error} fallback="The action could not be added." />
    </div>
  );
}

// ─── Shared form chrome ───────────────────────────────────────────────────────

function FormActions({
  onCancel,
  onSubmit,
  pending,
  disabled,
  submitTestId,
}: {
  onCancel: () => void;
  onSubmit: () => void;
  pending: boolean;
  disabled?: boolean;
  submitTestId: string;
}) {
  return (
    <div className="flex items-center gap-[var(--space-2)]">
      <button
        type="button"
        data-testid={submitTestId}
        onClick={onSubmit}
        disabled={pending || disabled}
        className={cn(
          'h-8 rounded-[var(--radius-md)] bg-[var(--color-primary)] px-[var(--space-3)]',
          'text-[var(--text-sm)] font-medium text-[var(--color-primary-contrast)]',
          'disabled:opacity-50',
        )}
      >
        {pending ? 'Saving…' : 'Save'}
      </button>
      <button
        type="button"
        onClick={onCancel}
        className="h-8 rounded-[var(--radius-md)] px-[var(--space-3)] text-[var(--text-sm)] text-[var(--color-text-muted)] hover:text-[var(--color-text)]"
      >
        Cancel
      </button>
    </div>
  );
}
