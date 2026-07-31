import { test, expect, type Browser, type Page } from '@playwright/test'
import {
  assertNoErrors,
  createSpace,
  getAuthToken,
  getCurrentUser,
  loginAs,
  seedUser,
} from './helpers/setup'

// P-W PR-B: the admin editor and the approval surfaces, end to end.
//
// Three journeys the phase brief names:
//   1. an admin adds a validator, the transition is refused with the NAMED
//      reason, the condition is satisfied, and it commits;
//   2. the approval round, both verdicts — approve lands the transition,
//      decline leaves the item where it was with the reason visible;
//   3. a contributor cannot reach the editor.
//
// Every name carries a run token. Every persona and every seeded row lands in
// one shared "E2E User" org that accumulates across parallel workers and repeat
// runs, so a bare count or a bare string would pass or fail on other runs'
// leftovers.

const RUN = `${process.env.E2E_RUN_TOKEN ?? 'wf'}-${Math.random().toString(36).slice(2, 8)}`

/**
 * Sign in as the owner of a BRAND NEW org.
 *
 * Not createUserAndLogin, which defaults the display name to "E2E User" and so
 * lands every persona in one shared org. Workflows are ORG-scoped objects, and
 * these tests configure rules on the org's default workflow — so in the shared
 * org a guard added by one test applies to every other test's tickets, and
 * survives into the next run of the whole suite. That is not a hypothetical:
 * the first version of this file leaked an approver from the approvals journey
 * into the validator journey, which then saw 202 where it expected 200.
 *
 * seedUser slugifies displayName into the org key, so a unique name is a unique
 * org. A fresh org is seeded with the two default workflows.
 */
async function ownerOfFreshOrg(page: Page, orgName: string) {
  await loginAs(page, seedUser({ displayName: orgName }))
  return getCurrentUser(page)
}

async function jsonHeaders(page: Page): Promise<Record<string, string>> {
  return { Authorization: `Bearer ${await getAuthToken(page)}`, 'Content-Type': 'application/json' }
}

async function api(page: Page, method: 'get' | 'post' | 'delete', path: string, data?: unknown) {
  const res = await page.request[method](path, { headers: await jsonHeaders(page), ...(data ? { data } : {}) })
  return res
}

/** The default ticket workflow's open -> in_progress edge, and its ids. */
async function ticketWorkflow(page: Page, orgId: string) {
  const wfRes = await api(page, 'get', `/api/v1/orgs/${orgId}/workflows`)
  const workflows = await wfRes.json()
  const wf = workflows.find((w: { applies_to: string; is_default: boolean }) =>
    w.applies_to === 'tickets' && w.is_default)
  if (!wf) throw new Error(`no default ticket workflow in org ${orgId}`)

  const statesRes = await api(page, 'get', `/api/v1/orgs/${orgId}/workflows/${wf.id}/states`)
  const states: { id: string; name: string }[] = await statesRes.json()
  const byName = Object.fromEntries(states.map((s) => [s.name, s.id]))

  const trRes = await api(page, 'get', `/api/v1/orgs/${orgId}/workflows/${wf.id}/transitions`)
  const transitions: { id: string; from_state_id: string; to_state_id: string }[] = await trRes.json()
  const edge = transitions.find(
    (t) => t.from_state_id === byName['open'] && t.to_state_id === byName['in_progress'],
  )
  if (!edge) throw new Error('the seeded ticket workflow must carry open -> in_progress')

  return { workflowId: wf.id as string, edgeId: edge.id as string }
}

async function createTicket(page: Page, orgId: string, spaceId: string, title: string): Promise<string> {
  const res = await api(page, 'post', `/api/v1/orgs/${orgId}/spaces/${spaceId}/tickets`, {
    title,
    priority: 'medium',
  })
  if (res.status() !== 201) throw new Error(`create ticket: ${res.status()} ${await res.text()}`)
  return (await res.json()).id
}

// ─── 1. A validator refuses by name, then is satisfied ────────────────────────

test('an admin adds a validator, the transition is refused with the named reason, then commits', async ({
  page,
}) => {
  const { orgId, userId } = await ownerOfFreshOrg(page, `WF Validator ${RUN}`)
  const spaceId = await createSpace(page, `Tiers ${RUN}`, 'beacon')
  const { edgeId } = await ticketWorkflow(page, orgId)

  // The admin configures the rule through the EDITOR, not through the API —
  // this journey is what the editor is for.
  await page.goto('/admin/workflows')
  await expect(page.getByTestId('admin-layout')).toBeVisible()

  const transitionRow = page.getByTestId(`transition-${edgeId}`)
  await transitionRow.scrollIntoViewIfNeeded()
  await transitionRow.click()

  await page.getByTestId(`add-guard-${edgeId}`).click()
  await page.getByTestId('guard-class').selectOption('validator')
  await page.getByTestId('guard-kind').selectOption('field_required')
  await page.getByTestId('guard-field').selectOption('assignee_id')

  // Declare the response BEFORE the click: the save button disables while the
  // request is in flight, so waiting on it going disabled races.
  const created = page.waitForResponse(
    (r) => r.url().includes(`/transitions/${edgeId}/guards`) && r.request().method() === 'POST',
  )
  await page.getByTestId('guard-submit').click()
  expect((await created).status()).toBe(201)

  // The rule now renders, in the admin's own words rather than as a wire value.
  await expect(page.getByText(/assignee must be filled in/i)).toBeVisible()

  // An unassigned ticket cannot make the move, and is told why.
  const ticketId = await createTicket(page, orgId, spaceId, `Unassigned ${RUN}`)
  await page.goto(`/beacon/${spaceId}/tickets/${ticketId}`)

  const refused = page.waitForResponse(
    (r) => r.url().includes(`/tickets/${ticketId}/status`) && r.request().method() === 'POST',
  )
  await page.getByLabel('Change status').selectOption('in_progress')
  expect((await refused).status()).toBe(422)

  // The NAMED reason, not a generic failure. ADR-0011's case for tier 1 rests
  // on the engine being able to explain itself; this is the last step where
  // that can be thrown away.
  await expect(page.getByTestId('status-outcome')).toContainText(/assignee/i)

  // And the select snapped back — the refusal is not a cosmetic message over a
  // change that happened anyway.
  await expect(page.getByLabel('Change status')).toHaveValue('open')

  // Satisfy the validator, and the same transition commits.
  const assigned = await api(page, 'post',
    `/api/v1/orgs/${orgId}/spaces/${spaceId}/tickets/${ticketId}/assign`, { assignee_id: userId })
  expect(assigned.status()).toBe(200)

  await page.reload()
  const accepted = page.waitForResponse(
    (r) => r.url().includes(`/tickets/${ticketId}/status`) && r.request().method() === 'POST',
  )
  await page.getByLabel('Change status').selectOption('in_progress')
  expect((await accepted).status()).toBe(200)
  await expect(page.getByLabel('Change status')).toHaveValue('in_progress')

  await assertNoErrors(page)
})

// ─── 2. The approval round, both verdicts ─────────────────────────────────────

test('an approval blocks the item, and both verdicts behave', async ({ page }) => {
  test.setTimeout(120_000)

  const { orgId, userId } = await ownerOfFreshOrg(page, `WF Approvals ${RUN}`)
  const spaceId = await createSpace(page, `Approvals ${RUN}`, 'beacon')
  const { workflowId, edgeId } = await ticketWorkflow(page, orgId)

  // Name THIS user the approver, so one browser context can both request and
  // decide. Who may decide is data, so this is the whole configuration.
  const approver = await api(page, 'post',
    `/api/v1/orgs/${orgId}/workflows/${workflowId}/transitions/${edgeId}/approvers`,
    { subject_type: 'user', subject_id: userId })
  expect(approver.status()).toBe(201)

  // ── decline: the item stays, and the reason is visible ──
  const declineTicket = await createTicket(page, orgId, spaceId, `Declined ${RUN}`)
  await page.goto(`/beacon/${spaceId}/tickets/${declineTicket}`)

  const gated = page.waitForResponse(
    (r) => r.url().includes(`/tickets/${declineTicket}/status`) && r.request().method() === 'POST',
  )
  await page.getByLabel('Change status').selectOption('in_progress')
  // 202, NOT an error and NOT a success. The item has not moved.
  expect((await gated).status()).toBe(202)

  await expect(page.getByTestId('approval-pending')).toBeVisible()
  await expect(page.getByTestId('approval-requester')).toBeVisible()
  await expect(page.getByTestId('approval-pending')).toContainText(/has not moved/i)
  await expect(page.getByLabel('Change status')).toHaveValue('open')

  await page.getByTestId('approval-decline').click()
  // A decline needs a reason, and the button says so before the round trip.
  await expect(page.getByTestId('approval-decline-submit')).toBeDisabled()
  await page.getByTestId('approval-decline-reason').fill(`frozen until Monday ${RUN}`)

  const declined = page.waitForResponse(
    (r) => r.url().includes('/decide') && r.request().method() === 'POST',
  )
  await page.getByTestId('approval-decline-submit').click()
  expect((await declined).status()).toBe(200)

  await expect(page.getByTestId('approval-declined')).toBeVisible()
  await expect(page.getByTestId('approval-decline-reason-text')).toContainText(`frozen until Monday ${RUN}`)
  // "Decline returns the item to the source status" is satisfied by it never
  // having left — so the copy says it STAYED, and the status agrees.
  await expect(page.getByTestId('approval-declined')).toContainText(/stayed in/i)
  await expect(page.getByLabel('Change status')).toHaveValue('open')

  // ── approve: the transition lands ──
  const approveTicket = await createTicket(page, orgId, spaceId, `Approved ${RUN}`)
  await page.goto(`/beacon/${spaceId}/tickets/${approveTicket}`)

  const gated2 = page.waitForResponse(
    (r) => r.url().includes(`/tickets/${approveTicket}/status`) && r.request().method() === 'POST',
  )
  await page.getByLabel('Change status').selectOption('in_progress')
  expect((await gated2).status()).toBe(202)

  await expect(page.getByTestId('approval-pending')).toBeVisible()
  const approved = page.waitForResponse(
    (r) => r.url().includes('/decide') && r.request().method() === 'POST',
  )
  await page.getByTestId('approval-approve').click()
  expect((await approved).status()).toBe(200)

  // The approval APPLIES the captured transition — and only then does the item
  // move. Assert the status, not the absence of the block.
  await expect(page.getByLabel('Change status')).toHaveValue('in_progress')
  await expect(page.getByTestId('approval-pending')).toHaveCount(0)

  await assertNoErrors(page)
})

// ─── 3. A contributor cannot reach the editor ─────────────────────────────────

async function memberPersona(browser: Browser, orgId: string, orgName: string, tag: string) {
  const context = await browser.newContext()
  const page = await context.newPage()
  // The SAME display name as the admin, so both land in the same fresh org.
  await loginAs(page, seedUser({ role: 'member', displayName: orgName, tag }))
  const who = await getCurrentUser(page)
  if (who.orgId !== orgId) {
    // seedUser slugifies the display name into an org key, so the DEFAULT name
    // is what lands a persona in the shared org. A persona in its own org would
    // be an org admin there and every assertion below would pass for the wrong
    // reason.
    await context.close()
    throw new Error(`persona ${tag} landed in org ${who.orgId}, expected ${orgId}`)
  }
  return { page, userId: who.userId, close: () => context.close() }
}

test('an org member who is not an admin cannot reach the workflow editor', async ({ page, browser }) => {
  test.setTimeout(120_000)

  const orgName = `WF Guard ${RUN}`
  const { orgId } = await ownerOfFreshOrg(page, orgName)

  // The persona must be a non-admin ORG MEMBER. An org admin is a middleware
  // BYPASS, so an admin persona would reach everything regardless of grants and
  // the test would assert nothing.
  const member = await memberPersona(browser, orgId, orgName, `wfmember-${RUN}`)
  try {
    await member.page.goto('/admin/workflows')

    // The branded not-found has no testid of its own — EmptyState renders none
    // — so the assertion is the copy plus the ABSENCE of the admin chrome.
    // .first() is required: the copy contains "not found" more than once.
    await expect(member.page.getByText(/page not found|doesn.t exist|not found/i).first()).toBeVisible()
    await expect(member.page.getByTestId('admin-layout')).toHaveCount(0)
    await expect(member.page.getByTestId('admin-tab-workflows')).toHaveCount(0)

    // Absence alone would be vacuous, so pair it with a sighting: the SAME
    // selectors resolve for the admin in the same org.
    await page.goto('/admin/workflows')
    await expect(page.getByTestId('admin-layout')).toBeVisible()
    await expect(page.getByTestId('admin-tab-workflows')).toBeVisible()

    // And the server refuses independently of the client. The mutation is
    // org-admin (403, not the P2.5 404 posture), and the READ is deliberately
    // org-member — so a non-admin seeing the guard list is correct, and only
    // the write must be refused.
    const { workflowId, edgeId } = await ticketWorkflow(page, orgId)
    const write = await api(member.page, 'post',
      `/api/v1/orgs/${orgId}/workflows/${workflowId}/transitions/${edgeId}/guards`,
      { guard_class: 'validator', kind: 'actor_is_assignee', position: 0 })
    expect(write.status()).toBe(403)
  } finally {
    await member.close()
  }
})
