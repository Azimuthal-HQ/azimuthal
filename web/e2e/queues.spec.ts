import { test, expect, type Browser, type Page } from '@playwright/test'
import {
  assertNoErrors,
  createUserAndLogin,
  getAuthToken,
  getCurrentUser,
  loginAs,
  seedUser,
} from './helpers/setup'

// P4 Beacon queues — the journeys that decide whether the surface is safe,
// not merely present. It is the sibling of saved-views.spec.ts and borrows its
// fixture idioms wholesale, because a queue IS a saved view with a space
// binding and a position.
//
// Two of these are the reason the file exists:
//
//  1. PER-AGENT RESULTS. ONE stored "Assigned to me" queue, two signed-in
//     agents in the same space, each seeing only their own assigned tickets.
//     That is the proof the `me` token resolves against the VIEWER and was
//     never frozen into the author's id at write time — which is what makes
//     one shared default set useful instead of a per-person copy of the same
//     question. Asserted through the rendered rows AND against the raw
//     /results payload, so a row that reached the browser and merely went
//     unpainted still fails.
//
//  2. THE CAPABILITY BOUNDARY. The refused persona is a CONTRIBUTOR, never a
//     viewer. ADR-0007 puts manage_queue at the agent role, and a viewer is
//     already refused upstream by RequireWriteFloor(CapCreateItems) — so a
//     "viewer is refused" test passes with the in-handler access.Can check
//     deleted and asserts the middleware rather than the gate. A contributor
//     is past the floor and short of the capability, which is the only subject
//     that can tell those two apart. An agent in the same space runs every
//     positive half, so the pair distinguishes "the gate works" from "nothing
//     works".
//
// Two seeding details are load-bearing throughout, and are not tidy-up-able:
//
//  - Every persona is a NON-ADMIN org member. An org admin reads and manages
//    every space through the middleware bypass, so an admin could never be the
//    reader who must be refused, and an admin as the reader who is allowed
//    would prove the bypass rather than the grant.
//  - Every space is created HIDDEN. Visibility 'org' contributes RoleViewer to
//    every org member (internal/core/access/explain.go), so on an org-visible
//    space a persona's access has two sources and the grant under test is only
//    one of them. Hidden makes the space grant the sole source, which is what
//    lets "contributor" mean contributor and nothing more.

// ---------------------------------------------------------------------------
// Seeding helpers
// ---------------------------------------------------------------------------

/** A token unique to one test, embedded in every name that test seeds. */
function runToken(): string {
  return `q${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
}

async function jsonHeaders(page: Page): Promise<Record<string, string>> {
  return {
    Authorization: `Bearer ${await getAuthToken(page)}`,
    'Content-Type': 'application/json',
  }
}

interface SeededSpace {
  id: string
  name: string
  key: string
}

/**
 * Creates a HIDDEN Beacon space and CONFIRMS the visibility took.
 *
 * The confirmation is not ceremony. Every persona below derives its whole
 * access from one space grant, and a create that silently fell back to the
 * default ('org', which is itself worth a viewer role) would give the
 * contributor persona a second source of access — quietly changing what the
 * capability journey is testing while leaving it green.
 */
async function createBeaconSpace(page: Page, orgId: string, name: string): Promise<SeededSpace> {
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
  const uniqueName = `${name} ${stamp}`
  const slug = uniqueName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  const key = ('QU' + Math.random().toString(36).replace(/[^a-z0-9]/g, '').toUpperCase()).slice(0, 8)

  const headers = await jsonHeaders(page)
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces`, {
    headers,
    data: { name: uniqueName, slug, key, type: 'beacon', visibility: 'hidden' },
  })
  if (res.status() !== 201) throw new Error(`create space: ${res.status()} ${await res.text()}`)
  const created = (await res.json()) as { id: string }

  const check = await page.request.get(`/api/v1/orgs/${orgId}/spaces/${created.id}`, { headers })
  if (check.status() !== 200) throw new Error(`read back space: ${check.status()} ${await check.text()}`)
  const space = (await check.json()) as { visibility?: string }
  if (space.visibility !== 'hidden') {
    throw new Error(`space ${uniqueName} seeded at visibility ${space.visibility}, wanted hidden`)
  }
  return { id: created.id, name: uniqueName, key }
}

/**
 * A user grant on a space, which is where a persona's role comes from.
 *
 * Space MEMBERSHIP is deliberately not used: the readable set comes from
 * space_grants unioned with org-visible spaces (ResolveAccessRows), and these
 * spaces are hidden — so a membership row would leave the persona unable to
 * read the space at all, and every "can read, cannot manage" assertion below
 * would pass for the wrong reason.
 */
async function grantSpaceRole(
  page: Page,
  orgId: string,
  spaceId: string,
  userId: string,
  role: 'viewer' | 'contributor' | 'agent',
): Promise<void> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/grants`, {
    headers: await jsonHeaders(page),
    data: { subject_type: 'user', subject_id: userId, role },
  })
  if (res.status() !== 201) throw new Error(`create grant: ${res.status()} ${await res.text()}`)
}

async function createTicket(
  page: Page,
  orgId: string,
  spaceId: string,
  title: string,
  assigneeId?: string,
): Promise<string> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/tickets`, {
    headers: await jsonHeaders(page),
    data: { title, priority: 'medium', ...(assigneeId ? { assignee_id: assigneeId } : {}) },
  })
  if (res.status() !== 201) throw new Error(`create ticket: ${res.status()} ${await res.text()}`)
  const body = (await res.json()) as { id: string }
  return body.id
}

interface WireQueue {
  id: string
  space_id: string
  position: number
  name: string
  description: string
  can_manage: boolean
  query: { filter?: { space_ids?: string[] } }
}

interface WireQueueList {
  queues: WireQueue[] | null
  can_manage: boolean
}

function queuePath(orgId: string, spaceId: string): string {
  return `/api/v1/orgs/${orgId}/spaces/${spaceId}/queues`
}

/** The queue list exactly as the wire sends it, for whoever this page is. */
async function listQueues(page: Page, orgId: string, spaceId: string): Promise<WireQueueList> {
  const res = await page.request.get(queuePath(orgId, spaceId), { headers: await jsonHeaders(page) })
  if (res.status() !== 200) throw new Error(`list queues: ${res.status()} ${await res.text()}`)
  const body = (await res.json()) as WireQueueList
  return { queues: body.queues ?? [], can_manage: body.can_manage }
}

/** Seeds the four defaults through the same guarded endpoint the button uses. */
async function seedDefaultQueues(page: Page, orgId: string, spaceId: string): Promise<number> {
  const res = await page.request.post(`${queuePath(orgId, spaceId)}/defaults`, {
    headers: await jsonHeaders(page),
  })
  if (res.status() !== 200) throw new Error(`seed defaults: ${res.status()} ${await res.text()}`)
  const body = (await res.json()) as { created: number | null }
  return body.created ?? 0
}

interface WireResults {
  results: Array<{ id: string; title: string; assignee_id: string | null }> | null
}

/** The raw /results payload for whoever this page is signed in as. */
async function fetchQueueResults(
  page: Page,
  orgId: string,
  spaceId: string,
  queueId: string,
): Promise<WireResults> {
  const res = await page.request.get(`${queuePath(orgId, spaceId)}/${queueId}/results`, {
    headers: await jsonHeaders(page),
  })
  if (res.status() !== 200) throw new Error(`queue results: ${res.status()} ${await res.text()}`)
  return (await res.json()) as WireResults
}

/** The four names, in the order CreateDefaults writes them. */
const DEFAULT_QUEUE_NAMES = ['All open', 'Assigned to me', 'Unassigned', 'Recently resolved']

interface Persona {
  page: Page
  userId: string
  orgId: string
  close: () => Promise<void>
}

/**
 * A NON-ADMIN org member holding exactly one role on one space, in their own
 * browser context.
 *
 * The grant is issued by `admin` AFTER the persona exists, because the grant
 * needs their user id — which is only knowable once the CLI has created them.
 */
async function spacePersona(
  browser: Browser,
  admin: Page,
  orgId: string,
  spaceId: string,
  role: 'viewer' | 'contributor' | 'agent',
  tag: string,
): Promise<Persona> {
  const context = await browser.newContext()
  const page = await context.newPage()
  await loginAs(page, seedUser({ role: 'member', tag }))
  const who = await getCurrentUser(page)
  if (who.orgId !== orgId) {
    // seedUser slugifies the display name into an org key, so the default name
    // must land every persona in the one shared org. If that ever changes, the
    // grant below would silently target a stranger.
    throw new Error(`persona ${tag} landed in org ${who.orgId}, expected ${orgId}`)
  }
  await grantSpaceRole(admin, orgId, spaceId, who.userId, role)
  return { page, userId: who.userId, orgId, close: () => context.close() }
}

// ---------------------------------------------------------------------------
// DOM readers
// ---------------------------------------------------------------------------

/** The queue names in the order the LIST paints them. */
async function domQueueOrder(page: Page): Promise<string[]> {
  const rows = page.getByTestId('queue-row')
  const count = await rows.count()
  const names: string[] = []
  for (let i = 0; i < count; i++) {
    names.push((await rows.nth(i).getByRole('link').first().innerText()).trim())
  }
  return names
}

/** The queue names in the order the SIDEBAR paints them. */
async function sidebarQueueOrder(page: Page): Promise<string[]> {
  const items = page.getByTestId('sidebar-queue-item')
  const count = await items.count()
  const names: string[] = []
  for (let i = 0; i < count; i++) {
    names.push((await items.nth(i).innerText()).trim())
  }
  return names
}

/** Result rows whose text contains `needle`. */
function resultRows(page: Page, needle: string) {
  return page.getByTestId('view-result-row').filter({ hasText: needle })
}

// ---------------------------------------------------------------------------
// 1. Create
// ---------------------------------------------------------------------------

test.describe('The queue builder', () => {
  test('an agent creates a queue, and it reaches both the list and the sidebar', async ({
    browser,
  }) => {
    test.setTimeout(120_000)
    const run = runToken()
    const queueName = `Escalations ${run}`

    const adminContext = await browser.newContext()
    const admin = await adminContext.newPage()
    await createUserAndLogin(admin)
    const { orgId } = await getCurrentUser(admin)
    const space = await createBeaconSpace(admin, orgId, 'Queues Create')

    const agent = await spacePersona(browser, admin, orgId, space.id, 'agent', 'q-author')

    await agent.page.goto(`/beacon/${space.id}/queues`)
    // exact: true is load-bearing. Playwright's accessible-name match is a
    // substring by default, so a bare 'Queues' also matches the empty state's
    // "No queues in this space yet" and trips strict mode. It only shows up on
    // a space that IS empty -- which is every space on CI's fresh database and
    // almost none on a shared local one that has accumulated fixtures. This
    // passed locally three times and failed on the first CI run for exactly
    // that reason.
    await expect(
      agent.page.getByRole('heading', { name: 'Queues', exact: true }),
    ).toBeVisible({ timeout: 15000 })
    await assertNoErrors(agent.page)

    // Empty first, so every count below is a change of state rather than a
    // state that was always there.
    await expect(agent.page.getByTestId('queue-row')).toHaveCount(0)
    await expect(agent.page.getByTestId('sidebar-queue-item')).toHaveCount(0)

    await agent.page.getByTestId('new-queue').click()
    await expect(agent.page).toHaveURL(new RegExp(`/beacon/${space.id}/queues/new$`), {
      timeout: 15000,
    })
    await expect(agent.page.getByTestId('query-filter-builder')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(agent.page)

    // The builder STATES the space binding where a saved view would offer a
    // space picker — a queue searches its own space and no other, and the
    // server rewrites the document to say so on every write.
    await expect(agent.page.getByTestId('view-spaces-bound')).toContainText(space.name)
    await expect(agent.page.getByTestId('view-spaces')).toHaveCount(0)
    await expect(agent.page.getByTestId('view-modules-locked')).toBeVisible()

    await agent.page.getByTestId('queue-name-input').fill(queueName)
    await agent.page
      .getByTestId('queue-description-input')
      .fill('Work that needs a second pair of eyes.')
    await agent.page.getByTestId('view-status-input').fill('in_progress')
    await agent.page.getByTestId('view-status-add').click()
    await expect(agent.page.getByTestId('view-status-in_progress')).toBeVisible()

    // Waiting on the save button going disabled would be wrong — it disables
    // while the request is in flight too. Wait on the response.
    const created = agent.page.waitForResponse(
      (r) =>
        r.request().method() === 'POST' &&
        new URL(r.url()).pathname.endsWith(`/spaces/${space.id}/queues`),
    )
    await agent.page.getByTestId('save-queue').click()
    expect((await created).status()).toBe(201)

    await expect(agent.page).toHaveURL(
      new RegExp(`/beacon/${space.id}/queues/[0-9a-f-]{36}$`),
      { timeout: 15000 },
    )
    const queueId = new URL(agent.page.url()).pathname.split('/').pop() as string
    await expect(agent.page.getByTestId('queue-name')).toHaveText(queueName)
    await assertNoErrors(agent.page)

    // The sidebar is a LAYOUT concern (ADR-0005) and picks the new queue up
    // without a reload.
    await expect(
      agent.page.getByTestId('sidebar-queue-item').filter({ hasText: queueName }),
    ).toHaveCount(1)

    await agent.page.goto(`/beacon/${space.id}/queues`)
    await expect(agent.page.getByTestId('queues-list')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(agent.page)
    const row = agent.page.getByTestId('queue-row').filter({ hasText: queueName })
    await expect(row).toHaveCount(1)
    await expect(row).toHaveAttribute('data-queue-id', queueId)

    // And on the wire: the queue is bound to the space it was created in, at
    // the end of that space's order.
    const wire = await listQueues(agent.page, orgId, space.id)
    expect(wire.can_manage).toBe(true)
    expect(wire.queues).toHaveLength(1)
    expect(wire.queues![0].id).toBe(queueId)
    expect(wire.queues![0].space_id).toBe(space.id)
    expect(wire.queues![0].position).toBe(0)
    expect(wire.queues![0].query.filter?.space_ids).toEqual([space.id])

    await agent.close()
    await adminContext.close()
  })
})

// ---------------------------------------------------------------------------
// 2. The default set is idempotent, and says so without reading as a failure
// ---------------------------------------------------------------------------

test.describe('The default queues', () => {
  test('a second press creates nothing, and the space still holds exactly four', async ({
    browser,
  }) => {
    test.setTimeout(120_000)

    const adminContext = await browser.newContext()
    const admin = await adminContext.newPage()
    await createUserAndLogin(admin)
    const { orgId } = await getCurrentUser(admin)
    const space = await createBeaconSpace(admin, orgId, 'Queues Defaults')

    // TWO agents, because the affordance lives in the empty state: once the
    // space has queues the button is gone, so a same-page second press cannot
    // exist. Two agents both looking at the empty space IS the double press —
    // and it is the exact race the ON CONFLICT DO NOTHING seeding is for.
    const first = await spacePersona(browser, admin, orgId, space.id, 'agent', 'q-def-first')
    const second = await spacePersona(browser, admin, orgId, space.id, 'agent', 'q-def-second')

    for (const p of [first, second]) {
      await p.page.goto(`/beacon/${space.id}/queues`)
      await expect(p.page.getByTestId('create-default-queues')).toBeVisible({ timeout: 15000 })
      await assertNoErrors(p.page)
    }

    // --- The first press.
    const firstPress = first.page.waitForResponse(
      (r) =>
        r.request().method() === 'POST' &&
        new URL(r.url()).pathname.endsWith(`/spaces/${space.id}/queues/defaults`),
    )
    await first.page.getByTestId('create-default-queues').click()
    const firstResponse = await firstPress
    expect(firstResponse.status()).toBe(200)
    expect(await firstResponse.json()).toEqual({ created: 4 })

    await expect(first.page.getByTestId('queues-list')).toBeVisible({ timeout: 15000 })
    await expect(first.page.getByTestId('queue-defaults-message')).toHaveText('Added 4 queues.')
    await expect(first.page.getByTestId('queue-row')).toHaveCount(4)
    expect(await domQueueOrder(first.page)).toEqual(DEFAULT_QUEUE_NAMES)
    // The affordance withdraws itself once the space has them.
    await expect(first.page.getByTestId('create-default-queues')).toHaveCount(0)
    await expect(first.page.getByTestId('queue-defaults-error')).toHaveCount(0)
    await assertNoErrors(first.page)

    // --- The second press, from a page that still shows the empty state.
    const secondPress = second.page.waitForResponse(
      (r) =>
        r.request().method() === 'POST' &&
        new URL(r.url()).pathname.endsWith(`/spaces/${space.id}/queues/defaults`),
    )
    await second.page.getByTestId('create-default-queues').click()
    const secondResponse = await secondPress
    expect(secondResponse.status()).toBe(200)
    expect(await secondResponse.json()).toEqual({ created: 0 })

    // "Nothing happened" is a legitimate outcome here, so it must read as
    // reassurance — not as an error panel, and not as silence.
    await expect(second.page.getByTestId('queue-defaults-message')).toHaveText(
      'This space already had all four. Nothing was duplicated.',
    )
    await expect(second.page.getByTestId('queue-defaults-error')).toHaveCount(0)
    await assertNoErrors(second.page)

    // And the list still holds exactly four — nothing was duplicated.
    await expect(second.page.getByTestId('queue-row')).toHaveCount(4)
    expect(await domQueueOrder(second.page)).toEqual(DEFAULT_QUEUE_NAMES)

    const wire = await listQueues(second.page, orgId, space.id)
    expect(wire.queues!.map((q) => q.name)).toEqual(DEFAULT_QUEUE_NAMES)
    expect(wire.queues!.map((q) => q.position)).toEqual([0, 1, 2, 3])

    await first.close()
    await second.close()
    await adminContext.close()
  })
})

// ---------------------------------------------------------------------------
// 3. Reorder
// ---------------------------------------------------------------------------

test.describe('Reordering a space’s queues', () => {
  test('a move sends the whole order, survives a reload, and leaves dense positions', async ({
    browser,
  }) => {
    test.setTimeout(120_000)

    const adminContext = await browser.newContext()
    const admin = await adminContext.newPage()
    await createUserAndLogin(admin)
    const { orgId } = await getCurrentUser(admin)
    const space = await createBeaconSpace(admin, orgId, 'Queues Reorder')
    const agent = await spacePersona(browser, admin, orgId, space.id, 'agent', 'q-reorder')

    expect(await seedDefaultQueues(agent.page, orgId, space.id)).toBe(4)
    const before = await listQueues(agent.page, orgId, space.id)
    expect(before.queues!.map((q) => q.name)).toEqual(DEFAULT_QUEUE_NAMES)
    const idByName = new Map(before.queues!.map((q) => [q.name, q.id]))

    await agent.page.goto(`/beacon/${space.id}/queues`)
    await expect(agent.page.getByTestId('queues-list')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(agent.page)
    expect(await domQueueOrder(agent.page)).toEqual(DEFAULT_QUEUE_NAMES)

    // Move the last queue up one slot.
    const expectedOrder = ['All open', 'Assigned to me', 'Recently resolved', 'Unassigned']

    const reordered = agent.page.waitForResponse(
      (r) =>
        r.request().method() === 'PUT' &&
        new URL(r.url()).pathname.endsWith(`/spaces/${space.id}/queues/order`),
    )
    await agent.page.getByRole('button', { name: 'Move Recently resolved up' }).click()
    const reorderResponse = await reordered
    expect(reorderResponse.status()).toBe(204)

    // THE REQUEST CARRIED THE FULL ORDER, not a swap. The endpoint takes a
    // permutation of the space's live queues and refuses anything less with a
    // 422 that changes nothing, so a client that sent two ids would leave the
    // ordering exactly as it was and the UI would look briefly right.
    const sent = reorderResponse.request().postDataJSON() as { queue_ids: string[] }
    expect(sent.queue_ids).toHaveLength(4)
    expect(new Set(sent.queue_ids).size).toBe(4)
    expect(sent.queue_ids).toEqual(expectedOrder.map((n) => idByName.get(n)))

    await expect(agent.page.getByTestId('queue-reorder-error')).toHaveCount(0)
    await expect
      .poll(() => domQueueOrder(agent.page), { timeout: 15000 })
      .toEqual(expectedOrder)

    // Persisted across a reload — not merely reordered in a client cache.
    await agent.page.reload()
    await expect(agent.page.getByTestId('queues-list')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(agent.page)
    expect(await domQueueOrder(agent.page)).toEqual(expectedOrder)
    // The sidebar reads the server's order straight from `position` and never
    // re-sorts by name, so it must agree.
    expect(await sidebarQueueOrder(agent.page)).toEqual(expectedOrder)

    // And the order the SERVER holds is the whole order: every queue moved to
    // a dense position, none left at a stale one.
    const after = await listQueues(agent.page, orgId, space.id)
    expect(after.queues!.map((q) => q.name)).toEqual(expectedOrder)
    expect(after.queues!.map((q) => q.position)).toEqual([0, 1, 2, 3])

    await agent.close()
    await adminContext.close()
  })
})

// ---------------------------------------------------------------------------
// 4. Per-agent results — the one the review reads first
// ---------------------------------------------------------------------------

test.describe('A queue resolves against whoever opens it', () => {
  test('one "Assigned to me" queue shows each agent only their own tickets', async ({ browser }) => {
    test.setTimeout(120_000)
    const run = runToken()

    const adminContext = await browser.newContext()
    const admin = await adminContext.newPage()
    await createUserAndLogin(admin)
    const { orgId } = await getCurrentUser(admin)

    // ONE space, and both agents hold the SAME role on it — so access cannot
    // be what separates their results. Only the `me` resolution can.
    const space = await createBeaconSpace(admin, orgId, 'Queues Me')
    const first = await spacePersona(browser, admin, orgId, space.id, 'agent', 'q-me-first')
    const second = await spacePersona(browser, admin, orgId, space.id, 'agent', 'q-me-second')

    const firstTitle = `${run} for the first agent`
    const secondTitle = `${run} for the second agent`
    const nobodyTitle = `${run} for nobody`
    const firstTicket = await createTicket(admin, orgId, space.id, firstTitle, first.userId)
    const secondTicket = await createTicket(admin, orgId, space.id, secondTitle, second.userId)
    const nobodyTicket = await createTicket(admin, orgId, space.id, nobodyTitle)

    expect(await seedDefaultQueues(first.page, orgId, space.id)).toBe(4)
    const listed = await listQueues(first.page, orgId, space.id)
    const mine = listed.queues!.find((q) => q.name === 'Assigned to me')
    expect(mine, 'the default set must contain "Assigned to me"').toBeTruthy()
    const queueId = mine!.id

    // The same stored queue, opened by two different people.
    const url = `/beacon/${space.id}/queues/${queueId}`

    await first.page.goto(url)
    await expect(first.page.getByTestId('queue-results')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(first.page)
    await expect(first.page.getByTestId('queue-name')).toHaveText('Assigned to me')
    await expect(resultRows(first.page, firstTitle)).toHaveCount(1)
    await expect(resultRows(first.page, secondTitle)).toHaveCount(0)
    await expect(resultRows(first.page, nobodyTitle)).toHaveCount(0)
    // The row reads "You" — the same resolution, visible in the rendering.
    await expect(resultRows(first.page, firstTitle)).toContainText('You')

    await second.page.goto(url)
    await expect(second.page.getByTestId('queue-results')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(second.page)
    await expect(second.page.getByTestId('queue-name')).toHaveText('Assigned to me')
    await expect(resultRows(second.page, secondTitle)).toHaveCount(1)
    await expect(resultRows(second.page, firstTitle)).toHaveCount(0)
    await expect(resultRows(second.page, nobodyTitle)).toHaveCount(0)
    await expect(resultRows(second.page, secondTitle)).toContainText('You')

    // Asserted again against the payloads, not the paint: a row that reached
    // the browser and merely went unrendered would pass the DOM checks above.
    const rawFirst = (await fetchQueueResults(first.page, orgId, space.id, queueId)).results ?? []
    const rawSecond = (await fetchQueueResults(second.page, orgId, space.id, queueId)).results ?? []
    expect(rawFirst.map((r) => r.id)).toEqual([firstTicket])
    expect(rawSecond.map((r) => r.id)).toEqual([secondTicket])
    expect(rawFirst.map((r) => r.id)).not.toContain(nobodyTicket)
    expect(rawSecond.map((r) => r.id)).not.toContain(nobodyTicket)
    // Every row really is the reader's own, per the wire's own assignee field.
    expect(rawFirst.every((r) => r.assignee_id === first.userId)).toBe(true)
    expect(rawSecond.every((r) => r.assignee_id === second.userId)).toBe(true)

    // The divergence is not two queues: the same id in both browsers.
    await expect(first.page).toHaveURL(new RegExp(`/queues/${queueId}$`))
    await expect(second.page).toHaveURL(new RegExp(`/queues/${queueId}$`))

    await first.close()
    await second.close()
    await adminContext.close()
  })
})

// ---------------------------------------------------------------------------
// 5. The capability boundary
// ---------------------------------------------------------------------------

test.describe('manage_queue sits at the agent role (ADR-0007)', () => {
  test('a contributor reads every queue, is offered no controls, and is refused by the server', async ({
    browser,
  }) => {
    test.setTimeout(120_000)

    const adminContext = await browser.newContext()
    const admin = await adminContext.newPage()
    await createUserAndLogin(admin)
    const { orgId } = await getCurrentUser(admin)
    const space = await createBeaconSpace(admin, orgId, 'Queues Capability')

    // THE PERSONA IS A CONTRIBUTOR, AND THAT IS THE WHOLE POINT — see the note
    // at the top of this file. The agent alongside is what distinguishes "the
    // gate works" from "nothing works".
    const contributor = await spacePersona(
      browser, admin, orgId, space.id, 'contributor', 'q-contrib',
    )
    const agent = await spacePersona(browser, admin, orgId, space.id, 'agent', 'q-agent')

    expect(await seedDefaultQueues(agent.page, orgId, space.id)).toBe(4)
    const agentWire = await listQueues(agent.page, orgId, space.id)
    expect(agentWire.can_manage, 'an agent holds manage_queue').toBe(true)

    // --- can_manage comes back FALSE on the wire, which is the single
    // authority the UI reads. It never reproduces the capability rule.
    const contributorWire = await listQueues(contributor.page, orgId, space.id)
    expect(contributorWire.can_manage).toBe(false)
    expect(contributorWire.queues!.map((q) => q.name)).toEqual(DEFAULT_QUEUE_NAMES)
    expect(contributorWire.queues!.every((q) => q.can_manage === false)).toBe(true)

    // --- They READ the surface perfectly well.
    await contributor.page.goto(`/beacon/${space.id}/queues`)
    await expect(contributor.page.getByTestId('queues-list')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(contributor.page)
    expect(await domQueueOrder(contributor.page)).toEqual(DEFAULT_QUEUE_NAMES)
    expect(await sidebarQueueOrder(contributor.page)).toEqual(DEFAULT_QUEUE_NAMES)

    // --- And are offered no way to change them, in either surface.
    await expect(contributor.page.getByTestId('new-queue')).toHaveCount(0)
    await expect(contributor.page.getByTestId('edit-queue')).toHaveCount(0)
    await expect(contributor.page.getByTestId('delete-queue')).toHaveCount(0)
    await expect(contributor.page.getByTestId('queue-move-up')).toHaveCount(0)
    await expect(contributor.page.getByTestId('queue-move-down')).toHaveCount(0)
    await expect(contributor.page.getByTestId('sidebar-new-queue')).toHaveCount(0)

    // A queue is still a queue for them: it opens and resolves.
    const queueId = contributorWire.queues![0].id
    await contributor.page.goto(`/beacon/${space.id}/queues/${queueId}`)
    await expect(contributor.page.getByTestId('queue-name')).toHaveText('All open', {
      timeout: 15000,
    })
    await expect(contributor.page.getByTestId('edit-queue')).toHaveCount(0)
    await assertNoErrors(contributor.page)

    // --- Reaching the builder URL directly says so, early and calmly. The
    // server would answer 403; this is the same answer arriving sooner, and it
    // must not read as an error.
    await contributor.page.goto(`/beacon/${space.id}/queues/new`)
    await expect(contributor.page.getByText('Queues here are managed by agents')).toBeVisible({
      timeout: 15000,
    })
    await expect(contributor.page.getByTestId('save-queue')).toHaveCount(0)
    await expect(contributor.page.getByTestId('query-filter-builder')).toHaveCount(0)
    await expect(contributor.page.getByTestId('queue-error')).toHaveCount(0)
    await assertNoErrors(contributor.page)

    // --- THE SERVER REFUSES THEIR MUTATIONS. The hidden controls are a
    // courtesy; this is the boundary. Every request below is well-formed and
    // would succeed for the agent, so the only difference is the capability.
    const headers = await jsonHeaders(contributor.page)
    const base = queuePath(orgId, space.id)
    const validQuery = {
      v: 1,
      filter: { modules: ['beacon'] },
      sort: { field: 'updated_at', dir: 'desc' },
    }

    const createRes = await contributor.page.request.post(base, {
      headers,
      data: { name: 'Contributor queue', description: '', query: validQuery },
    })
    expect(createRes.status(), await createRes.text()).toBe(403)

    const defaultsRes = await contributor.page.request.post(`${base}/defaults`, { headers })
    expect(defaultsRes.status(), await defaultsRes.text()).toBe(403)

    // The full, correct permutation — so a 422 could not be mistaken for the
    // refusal, and the 403 is unambiguously the capability gate.
    const orderRes = await contributor.page.request.put(`${base}/order`, {
      headers,
      data: { queue_ids: contributorWire.queues!.map((q) => q.id) },
    })
    expect(orderRes.status(), await orderRes.text()).toBe(403)

    const patchRes = await contributor.page.request.patch(`${base}/${queueId}`, {
      headers,
      data: { name: 'Renamed by a contributor', description: '', query: validQuery },
    })
    expect(patchRes.status(), await patchRes.text()).toBe(403)

    const deleteRes = await contributor.page.request.delete(`${base}/${queueId}`, { headers })
    expect(deleteRes.status(), await deleteRes.text()).toBe(403)

    // And nothing they attempted took effect.
    const afterWire = await listQueues(agent.page, orgId, space.id)
    expect(afterWire.queues!.map((q) => q.name)).toEqual(DEFAULT_QUEUE_NAMES)
    expect(afterWire.queues!.map((q) => q.position)).toEqual([0, 1, 2, 3])

    await contributor.close()
    await agent.close()
    await adminContext.close()
  })
})
