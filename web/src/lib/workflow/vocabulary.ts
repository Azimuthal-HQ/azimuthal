/**
 * The ADR-0011 workflow tier vocabularies, mirrored from Go.
 *
 * `internal/core/workflow/guard.go` names four things that depend on this list
 * and will drift the moment it is duplicated — the API validator, migration
 * 046's CHECK constraints, the admin UI's pickers, and the Jira importer — and
 * it names THIS FILE's test as the thing that holds the mirror equal:
 *
 *     the admin UI's pickers, which may only offer what this file enumerates
 *     (mirrored in TypeScript and held equal by web/src/lib/workflow/guards.test.ts)
 *
 * That comment described intent rather than fact until this PR: neither the
 * mirror nor the test existed. Both do now, and `guards.test.ts` fails in both
 * directions — a value added to Go and not here, or here and not in Go.
 *
 * # Why a mirror at all, rather than fetching the vocabulary
 *
 * An endpoint that served the list would make the picker correct by
 * construction, and it is the obvious design. It is not the one here, for the
 * reason ADR-0012's editor vocabulary is not fetched either: the values are
 * compiled into the UI's *behaviour*, not just its options. A `field_required`
 * guard needs a label and a field-specific input; `actor_in_team` needs a team
 * picker. A vocabulary arriving at runtime would still need every one of those
 * written by hand, so the fetch would buy a list that agrees while the code
 * around it did not — the drift moved somewhere harder to see. The test is the
 * cheaper and louder answer.
 *
 * # Everything here is CLOSED
 *
 * There is no free-text predicate anywhere in this feature, and none may be
 * added. ADR-0011 permanently refuses scripting — "no Groovy, no JavaScript
 * hooks, no user-supplied code, no plugin execution — at any tier, under any
 * framing, in any edition, now or later" — and that refusal is only real if the
 * form cannot become a language by accident. The vocabulary IS the form.
 */

// ─── Guards (tier 1) ──────────────────────────────────────────────────────────

/**
 * Which half of tier 1 a guard belongs to.
 *
 * A condition HIDES the transition when transitions are listed; a validator
 * OFFERS it and refuses it by name. The same kind can be either — see
 * GUARD_KINDS — so this is a separate axis, not a second vocabulary.
 */
export const GUARD_CLASSES = ['condition', 'validator'] as const;
export type GuardClass = (typeof GUARD_CLASSES)[number];

/**
 * The predicate a guard asks. Shared by BOTH classes.
 *
 * A UI that models conditions and validators as two different kind lists
 * invents a distinction the engine does not have.
 */
export const GUARD_KINDS = [
  'actor_is_assignee',
  'actor_in_team',
  'actor_has_capability',
  'field_required',
] as const;
export type GuardKind = (typeof GUARD_KINDS)[number];

/**
 * The fields `field_required` can require.
 *
 * Every member exists with the same meaning on both tickets and project items,
 * and every member is genuinely emptiable. `priority` is deliberately absent:
 * it is NOT NULL with a default on both tables, so requiring it would assert
 * something that cannot be false.
 *
 * NOTE the asymmetry with POST_FIELD_KEYS below — guards READ four fields,
 * post-functions WRITE two. Offering this list in a post-function picker gets a
 * 400 from ValidatePostFunction.
 */
export const GUARD_FIELD_KEYS = ['assignee_id', 'due_at', 'description', 'labels'] as const;
export type GuardFieldKey = (typeof GUARD_FIELD_KEYS)[number];

/**
 * The capabilities an `actor_has_capability` guard may name.
 *
 * A SUBSET of the capability model, not the whole table: most capabilities are
 * meaningless as a transition guard (gating on `read_items` asserts nothing —
 * the actor already read the item to transition it). Populating this picker
 * from the full capability list ships values the server refuses.
 *
 * It is also the one vocabulary migration 046 does NOT constrain: the column is
 * a bare `capability TEXT`, so ValidateGuard on the write path is the only
 * thing that catches a fifth value.
 */
export const GUARD_CAPABILITIES = [
  'edit_any_item',
  'transition_any_item',
  'manage_queue',
  'manage_space',
] as const;
export type GuardCapability = (typeof GUARD_CAPABILITIES)[number];

// ─── Post-functions (tier 3) ──────────────────────────────────────────────────

/**
 * The actions a transition can perform. Fixed in code; configuration cannot
 * extend it.
 *
 * ADR-0011 names two more — add a comment, transition a linked item — which do
 * not exist at any layer and are refused by
 * workflow_transition_post_functions_kind_valid. Do not offer them.
 */
export const POST_FUNCTION_KINDS = ['assign_to', 'set_field'] as const;
export type PostFunctionKind = (typeof POST_FUNCTION_KINDS)[number];

/**
 * The fields `set_field` may write. NARROWER than GUARD_FIELD_KEYS on purpose.
 *
 * `description` is readable by a guard but not writable — a post-function that
 * overwrote author prose is a different and worse thing than one that sets a
 * date — and `assignee_id` belongs to `assign_to`.
 */
export const POST_FIELD_KEYS = ['due_at', 'labels'] as const;
export type PostFieldKey = (typeof POST_FIELD_KEYS)[number];

// ─── Approvals (tier 2) ───────────────────────────────────────────────────────

/**
 * Who may be named as an approver.
 *
 * TWO values, not ADR-0011's three. "role" is not representable in this product
 * — a space role has no user-set resolution query, and a team role is metadata
 * explicitly forbidden as a permission input — and CreateApprover 400s on it.
 * Do not add a third on the strength of the ADR.
 */
export const APPROVER_SUBJECT_TYPES = ['user', 'team'] as const;
export type ApproverSubjectType = (typeof APPROVER_SUBJECT_TYPES)[number];

/**
 * The recorded outcome of an approval request.
 *
 * There is no third value. "Pending" is the ABSENCE of a decision
 * (`decided_at IS NULL`), not a member of this list, and "expired" is
 * deliberately absent because nothing times an approval out.
 */
export const DECISIONS = ['approved', 'declined'] as const;
export type Decision = (typeof DECISIONS)[number];

/**
 * Which table an approval's item lives in.
 *
 * "item", NOT "project_item" — tickets and project items stay separate tables
 * (ADR-0003) and this discriminator uses the audit log's words for the same two
 * things. Note that Workflow.applies_to spells the same concept
 * "project_items"; they are different vocabularies and must not be swapped.
 */
export const APPROVAL_ENTITY_TYPES = ['ticket', 'item'] as const;
export type ApprovalEntityType = (typeof APPROVAL_ENTITY_TYPES)[number];

// ─── Human labels ─────────────────────────────────────────────────────────────

/**
 * Labels are UI copy and are deliberately NOT part of the mirrored vocabulary —
 * `guards.test.ts` checks the wire values, not these. They are keyed by the
 * wire value so a missing label is a type error rather than a blank row.
 */
export const GUARD_KIND_LABEL: Record<GuardKind, string> = {
  actor_is_assignee: 'Only the assignee',
  actor_in_team: 'Only members of a team',
  actor_has_capability: 'Only people with a capability',
  field_required: 'A field must be filled in',
};

export const GUARD_CLASS_LABEL: Record<GuardClass, string> = {
  condition: 'Condition — hides the transition',
  validator: 'Validator — refuses it, with a reason',
};

export const GUARD_FIELD_LABEL: Record<GuardFieldKey, string> = {
  assignee_id: 'Assignee',
  due_at: 'Due date',
  description: 'Description',
  labels: 'Labels',
};

export const GUARD_CAPABILITY_LABEL: Record<GuardCapability, string> = {
  edit_any_item: 'Edit any item',
  transition_any_item: 'Transition any item',
  manage_queue: 'Manage the queue',
  manage_space: 'Manage the space',
};

export const POST_FUNCTION_KIND_LABEL: Record<PostFunctionKind, string> = {
  assign_to: 'Assign to a person',
  set_field: 'Set a field',
};

export const POST_FIELD_LABEL: Record<PostFieldKey, string> = {
  due_at: 'Due date',
  labels: 'Labels',
};

/**
 * Which parameter a guard kind takes. Exactly one, or none.
 *
 * Mirrors migration 046's shape CHECK and ValidateGuard's switch: sending a
 * parameter a kind does not take is a 400, and omitting the one it needs is
 * another. The editor uses this to decide which input to render, so the form
 * cannot express a shape the server refuses.
 */
export const GUARD_KIND_PARAM: Record<GuardKind, 'none' | 'team' | 'capability' | 'field'> = {
  actor_is_assignee: 'none',
  actor_in_team: 'team',
  actor_has_capability: 'capability',
  field_required: 'field',
};
