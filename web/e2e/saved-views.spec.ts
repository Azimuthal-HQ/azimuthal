import { test, expect, type Browser, type Page } from '@playwright/test'
import {
  assertNoErrors,
  createUserAndLogin,
  getAuthToken,
  getCurrentUser,
  loginAs,
  seedUser,
} from './helpers/setup'

// P4 saved views (ADR-0009, ADR-0010) — the journeys that decide whether the
// feature is safe, not merely present.
//
// Two of them are the reason this file exists:
//
//  1. THE HIDDEN-SPACE LEAK. A view stores a question, and the question is
//     resolved against the READER. The same saved view, opened by someone who
//     cannot read a space, must not return that space's work — and must still
//     return the work they CAN read, or the test cannot tell "correctly
//     filtered" from "returned nothing at all". Both halves are asserted, and
//     the negative half is asserted twice: once through the rendered list and
//     once against the raw /results JSON, so a row that was fetched and merely
//     not painted would still fail.
//
//  2. THE `me` DIVERGENCE. One view, shared org-wide, filtering on the literal
//     token "me". Two signed-in people open it and each sees only their own
//     assigned work. That is the proof that sharing a view shares the
//     DEFINITION and never the results.
//
// Locator discipline follows the rest of the suite: every E2E user seeded with
// the default display name lands in ONE shared org, which accumulates spaces,
// tickets and org-visible views across runs — so every assertion is scoped by a
// per-run token that appears in the seeded titles, never by a bare count.

// ---------------------------------------------------------------------------
// Seeding helpers
// ---------------------------------------------------------------------------

/**
 * A token unique to one test, embedded in every title that test seeds.
 *
 * It doubles as the saved views' `text` filter, which is a literal substring
 * match against the title — so a view filtered on the token sees this test's
 * rows and nothing else in the shared org. It carries no LIKE metacharacter,
 * so `access.EscapeLike` has nothing to change.
 */
function runToken(): string {
  return `sv${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
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
 * Creates a space at a chosen visibility and CONFIRMS the visibility took.
 *
 * The confirmation is not ceremony. The leak journey below distinguishes a
 * space the second reader can open from one they cannot, and it does so purely
 * through `visibility` — a create that silently fell back to the default would
 * make the leak assertion pass while testing nothing.
 *
 * Unlike helpers/setup's createSpace this does not navigate: these spaces are
 * fixtures for a view, and three of them per test would be three page loads
 * nobody asserts anything about.
 */
async function createSpaceViaAPI(
  page: Page,
  orgId: string,
  name: string,
  type: 'beacon' | 'vector',
  visibility: 'hidden' | 'discoverable' | 'org',
): Promise<SeededSpace> {
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
  const uniqueName = `${name} ${stamp}`
  const slug = uniqueName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  const key = ('SV' + Math.random().toString(36).replace(/[^a-z0-9]/g, '').toUpperCase()).slice(0, 8)

  const headers = await jsonHeaders(page)
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces`, {
    headers,
    data: { name: uniqueName, slug, key, type, visibility },
  })
  if (res.status() !== 201) throw new Error(`create space: ${res.status()} ${await res.text()}`)
  const created = (await res.json()) as { id: string }

  const check = await page.request.get(`/api/v1/orgs/${orgId}/spaces/${created.id}`, { headers })
  if (check.status() !== 200) throw new Error(`read back space: ${check.status()} ${await check.text()}`)
  const space = (await check.json()) as { visibility?: string }
  if (space.visibility !== visibility) {
    throw new Error(`space ${uniqueName} seeded at visibility ${space.visibility}, wanted ${visibility}`)
  }
  return { id: created.id, name: uniqueName, key }
}

async function deleteSpaceViaAPI(page: Page, orgId: string, spaceId: string): Promise<void> {
  const res = await page.request.delete(`/api/v1/orgs/${orgId}/spaces/${spaceId}`, {
    headers: await jsonHeaders(page),
  })
  if (res.status() !== 204 && res.status() !== 200) {
    throw new Error(`delete space: ${res.status()} ${await res.text()}`)
  }
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

/**
 * A user grant on a space. Space MEMBERSHIP is deliberately not used here:
 * the readable set comes from space_grants unioned with org-visible spaces
 * (ResolveAccessRows), so adding a member would leave the person unable to read
 * the space and the "insider" half of the leak journey would assert nothing.
 */
async function grantUserOnSpace(
  page: Page,
  orgId: string,
  spaceId: string,
  userId: string,
  role: 'viewer' | 'agent',
): Promise<void> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/grants`, {
    headers: await jsonHeaders(page),
    data: { subject_type: 'user', subject_id: userId, role },
  })
  if (res.status() !== 201) throw new Error(`create grant: ${res.status()} ${await res.text()}`)
}

interface QueryDocLike {
  v: number
  filter: Record<string, unknown>
  sort: { field: string; dir: string }
}

/** The stored filter document, spelled the way the wire spells it. */
function queryDoc(filter: Record<string, unknown>): QueryDocLike {
  return { v: 1, filter, sort: { field: 'updated_at', dir: 'desc' } }
}

async function createViewViaAPI(
  page: Page,
  orgId: string,
  req: {
    name: string
    description?: string
    query: QueryDocLike
    visibility: 'private' | 'team' | 'org'
    visibility_team_id?: string | null
  },
): Promise<{ id: string; name: string }> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/views`, {
    headers: await jsonHeaders(page),
    data: {
      description: '',
      visibility_team_id: null,
      ...req,
    },
  })
  if (res.status() !== 201) throw new Error(`create view: ${res.status()} ${await res.text()}`)
  const body = (await res.json()) as { id: string; name: string }
  return body
}

interface ResultsPage {
  results: Array<{ id: string; title: string; space_id: string; assignee_id: string | null }> | null
}

/** The raw /results payload for whoever this page is signed in as. */
async function fetchResults(page: Page, orgId: string, viewId: string): Promise<ResultsPage> {
  const res = await page.request.get(`/api/v1/orgs/${orgId}/views/${viewId}/results`, {
    headers: await jsonHeaders(page),
  })
  if (res.status() !== 200) throw new Error(`view results: ${res.status()} ${await res.text()}`)
  return (await res.json()) as ResultsPage
}

interface Persona {
  page: Page
  userId: string
  orgId: string
  close: () => Promise<void>
}

/**
 * A NON-ADMIN org member in their own browser context.
 *
 * Non-admin is load-bearing everywhere below: an org admin reads every space in
 * the org through the middleware bypass, so an admin can never be the reader
 * who must be refused, and an admin as the reader who is allowed would prove
 * the bypass rather than the grant.
 */
async function memberPersona(browser: Browser, tag: string): Promise<Persona> {
  const context = await browser.newContext()
  const page = await context.newPage()
  await loginAs(page, seedUser({ role: 'member', tag }))
  const { userId, orgId } = await getCurrentUser(page)
  return { page, userId, orgId, close: () => context.close() }
}

/** Result rows whose text contains `needle`. */
function resultRows(page: Page, needle: string) {
  return page.getByTestId('view-result-row').filter({ hasText: needle })
}

// ---------------------------------------------------------------------------
// 1. The hidden-space leak
// ---------------------------------------------------------------------------

test.describe('Saved views resolve against the reader', () => {
  test('a shared view hides a ticket from a reader who cannot open its space, and shows it to one who can', async ({
    browser,
  }) => {
    test.setTimeout(120_000)
    const run = runToken()

    // --- The author: an org admin, who seeds both spaces and owns the view.
    const authorContext = await browser.newContext()
    const author = await authorContext.newPage()
    await createUserAndLogin(author)
    const { orgId } = await getCurrentUser(author)

    const openSpace = await createSpaceViaAPI(author, orgId, 'Views Open', 'beacon', 'org')
    const hiddenSpace = await createSpaceViaAPI(author, orgId, 'Views Hidden', 'beacon', 'hidden')

    // Two tickets, one in each space, both matching the run token — so the
    // view's filter cannot be the thing that separates them. Only access can.
    const openTitle = `${run} open ticket`
    const secretTitle = `${run} secret ticket`
    await createTicket(author, orgId, openSpace.id, openTitle)
    const secretId = await createTicket(author, orgId, hiddenSpace.id, secretTitle)

    // One view, shared org-wide, scoped to no space at all: "everything I can
    // read that matches this token". Naming the spaces explicitly would let a
    // scope check rather than an access check do the filtering.
    const view = await createViewViaAPI(author, orgId, {
      name: `Leak probe ${run}`,
      query: queryDoc({ modules: ['beacon'], text: run }),
      visibility: 'org',
    })

    // --- The two readers. Both are plain members; they differ in exactly one
    // thing, a viewer grant on the hidden space.
    const insider = await memberPersona(browser, 'insider')
    const outsider = await memberPersona(browser, 'outsider')
    await grantUserOnSpace(author, orgId, hiddenSpace.id, insider.userId, 'viewer')

    // --- THE POSITIVE HALF. Without it, the negative half below cannot tell a
    // correctly filtered view from a view that returned nothing.
    await insider.page.goto(`/views/${view.id}`)
    await expect(insider.page.getByTestId('view-results')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(insider.page)
    await expect(resultRows(insider.page, secretTitle)).toHaveCount(1)
    await expect(resultRows(insider.page, openTitle)).toHaveCount(1)

    // --- THE NEGATIVE HALF. Same view, same filter, less access.
    await outsider.page.goto(`/views/${view.id}`)
    await expect(outsider.page.getByTestId('view-results')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(outsider.page)
    // The view demonstrably ran for them...
    await expect(resultRows(outsider.page, openTitle)).toHaveCount(1)
    // ...and the ticket in the space they cannot open is not in it.
    await expect(resultRows(outsider.page, secretTitle)).toHaveCount(0)

    // Asserted again against the payload, not the paint: a row that reached the
    // browser and merely went unrendered would pass the DOM check above.
    const raw = await fetchResults(outsider.page, orgId, view.id)
    const ids = (raw.results ?? []).map((r) => r.id)
    expect(ids).not.toContain(secretId)
    expect((raw.results ?? []).map((r) => r.title)).toContain(openTitle)

    await insider.close()
    await outsider.close()
    await authorContext.close()
  })

  // -------------------------------------------------------------------------
  // 2. The two-viewer `me` divergence
  // -------------------------------------------------------------------------

  test('one org-wide view filtered on "me" shows each reader only their own work', async ({
    browser,
  }) => {
    test.setTimeout(120_000)
    const run = runToken()

    const authorContext = await browser.newContext()
    const author = await authorContext.newPage()
    await createUserAndLogin(author)
    const { orgId } = await getCurrentUser(author)

    // ONE space, org-visible, so access is identical for both readers and the
    // only thing that can separate their results is the `me` resolution.
    const space = await createSpaceViaAPI(author, orgId, 'Views Me', 'beacon', 'org')

    const first = await memberPersona(browser, 'me-first')
    const second = await memberPersona(browser, 'me-second')

    const firstTitle = `${run} for the first reader`
    const secondTitle = `${run} for the second reader`
    const nobodyTitle = `${run} for nobody`
    await createTicket(author, orgId, space.id, firstTitle, first.userId)
    await createTicket(author, orgId, space.id, secondTitle, second.userId)
    await createTicket(author, orgId, space.id, nobodyTitle)

    // ONE view. The literal token is stored; it is never substituted for the
    // author's id, which is what would freeze a shared view to one person.
    const view = await createViewViaAPI(author, orgId, {
      name: `My work ${run}`,
      query: queryDoc({ modules: ['beacon'], text: run, assignees: ['me'] }),
      visibility: 'org',
    })

    await first.page.goto(`/views/${view.id}`)
    await expect(first.page.getByTestId('view-results')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(first.page)
    await expect(resultRows(first.page, firstTitle)).toHaveCount(1)
    await expect(resultRows(first.page, secondTitle)).toHaveCount(0)
    await expect(resultRows(first.page, nobodyTitle)).toHaveCount(0)
    // The row reads "You" — the same resolution, visible in the rendering.
    await expect(resultRows(first.page, firstTitle)).toContainText('You')

    await second.page.goto(`/views/${view.id}`)
    await expect(second.page.getByTestId('view-results')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(second.page)
    await expect(resultRows(second.page, secondTitle)).toHaveCount(1)
    await expect(resultRows(second.page, firstTitle)).toHaveCount(0)
    await expect(resultRows(second.page, nobodyTitle)).toHaveCount(0)
    await expect(resultRows(second.page, secondTitle)).toContainText('You')

    // The same view id in both browsers: the divergence is not two views.
    await expect(first.page).toHaveURL(new RegExp(`/views/${view.id}$`))
    await expect(second.page).toHaveURL(new RegExp(`/views/${view.id}$`))

    await first.close()
    await second.close()
    await authorContext.close()
  })
})

// ---------------------------------------------------------------------------
// 3. Create, share org-wide, and open as somebody else
// ---------------------------------------------------------------------------

test.describe('The view builder', () => {
  test('an org-wide view built in the UI reaches a second person\'s list and opens', async ({
    browser,
  }) => {
    test.setTimeout(120_000)
    const run = runToken()
    const viewName = `Shared org view ${run}`

    const authorContext = await browser.newContext()
    const author = await authorContext.newPage()
    await createUserAndLogin(author)
    const { orgId } = await getCurrentUser(author)
    const space = await createSpaceViaAPI(author, orgId, 'Views Shared', 'beacon', 'org')
    const ticketTitle = `${run} shared work`
    await createTicket(author, orgId, space.id, ticketTitle)

    await author.goto('/views/new')
    await expect(author.getByTestId('query-filter-builder')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(author)

    await author.getByTestId('view-name').fill(viewName)
    await author.getByTestId('view-text').fill(run)

    // SegmentedControl segments are radios, not buttons.
    await author.getByTestId('view-visibility').getByRole('radio', { name: 'Organisation' }).click()
    await expect(
      author.getByTestId('view-visibility').getByRole('radio', { name: 'Organisation' }),
    ).toHaveAttribute('aria-checked', 'true')

    // Waiting on the save button going disabled would be wrong — it disables
    // while the request is in flight too. Wait on the response.
    const createResponse = author.waitForResponse(
      (r) => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/views'),
    )
    await author.getByTestId('save-view').click()
    expect((await createResponse).status()).toBe(201)

    await expect(author).toHaveURL(/\/views\/[0-9a-f-]{36}$/, { timeout: 15000 })
    const viewId = new URL(author.url()).pathname.split('/').pop() as string
    await expect(author.getByRole('heading', { name: viewName })).toBeVisible()
    await assertNoErrors(author)

    // --- A second person, a plain member, finds it in their own list.
    const reader = await memberPersona(browser, 'reader')
    await reader.page.goto('/views')
    await expect(reader.page.getByTestId('views-list')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(reader.page)

    const row = reader.page.getByTestId('view-row').filter({ hasText: viewName })
    await expect(row).toHaveCount(1)
    // Provenance: it is somebody else's, and they are not offered its controls.
    await expect(row.getByTestId('view-owner-chip')).toHaveAttribute('data-owner', 'other')
    await expect(row.getByTestId('view-visibility-chip')).toHaveAttribute('data-visibility', 'org')
    await expect(row.getByTestId('edit-view')).toHaveCount(0)
    await expect(row.getByTestId('delete-view')).toHaveCount(0)

    await row.getByRole('link', { name: viewName }).click()
    await expect(reader.page).toHaveURL(new RegExp(`/views/${viewId}$`), { timeout: 15000 })
    await expect(reader.page.getByRole('heading', { name: viewName })).toBeVisible()
    await assertNoErrors(reader.page)
    // The definition travelled; the results are still resolved for them, and
    // this space is org-visible so they legitimately see the row.
    await expect(resultRows(reader.page, ticketTitle)).toHaveCount(1)

    await reader.close()
    await authorContext.close()
  })

  // -------------------------------------------------------------------------
  // 4. Save-as-view from the Vector backlog
  // -------------------------------------------------------------------------

  test('save-as-view carries the backlog\'s current filters into the builder', async ({ page }) => {
    test.setTimeout(120_000)
    const run = runToken()

    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)
    const space = await createSpaceViaAPI(page, orgId, 'Views Backlog', 'vector', 'org')

    // The suggested name is "Backlog in <space name>", which needs useSpace to
    // have resolved. Wait on that exact response rather than on a proxy for it.
    const spaceLoaded = page.waitForResponse(
      (r) =>
        r.request().method() === 'GET' &&
        new URL(r.url()).pathname.endsWith(`/spaces/${space.id}`) &&
        r.ok(),
    )
    await page.goto(`/vector/${space.id}/backlog`)
    await spaceLoaded
    await assertNoErrors(page)

    const search = page.getByPlaceholder('Search items...')
    await expect(search).toBeVisible({ timeout: 15000 })
    await search.fill(run)

    await page.getByTestId('save-as-view').click()
    await expect(page).toHaveURL(/\/views\/new$/, { timeout: 15000 })
    await expect(page.getByTestId('query-filter-builder')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(page)

    // The page's text box became the view's text term...
    await expect(page.getByTestId('view-text')).toHaveValue(run)
    // ...the suggested name names the space it came from...
    await expect(page.getByTestId('view-name')).toHaveValue(`Backlog in ${space.name}`)
    // ...the module is Vector alone, because a backlog is Vector alone...
    const modules = page.getByTestId('view-modules')
    await expect(modules.locator('button', { hasText: 'Vector' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    await expect(modules.locator('button', { hasText: 'Beacon' })).toHaveAttribute(
      'aria-pressed',
      'false',
    )
    // ...and the space is the one scope named.
    await expect(
      page.getByTestId('view-spaces').locator('button', { hasText: space.name }),
    ).toHaveAttribute('aria-pressed', 'true')
  })
})

// ---------------------------------------------------------------------------
// 5. A view whose only scoped space was deleted
// ---------------------------------------------------------------------------

test.describe('Scope unavailable (ADR-0009 case C1)', () => {
  test('a view whose only space was deleted still opens, and reads as degraded rather than broken', async ({
    page,
  }) => {
    test.setTimeout(120_000)
    const run = runToken()

    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)
    const doomed = await createSpaceViaAPI(page, orgId, 'Views Doomed', 'beacon', 'org')
    await createTicket(page, orgId, doomed.id, `${run} about to vanish`)

    const viewName = `Scoped to a doomed space ${run}`
    const view = await createViewViaAPI(page, orgId, {
      name: viewName,
      query: queryDoc({ modules: ['beacon'], space_ids: [doomed.id] }),
      visibility: 'private',
    })

    // It is a perfectly ordinary view first — so the assertions after the
    // deletion are a change of state, not a state that was always there.
    await page.goto(`/views/${view.id}`)
    await expect(page.getByTestId('view-results')).toBeVisible({ timeout: 15000 })
    await expect(page.getByTestId('view-scope-unavailable')).toHaveCount(0)
    await assertNoErrors(page)

    await deleteSpaceViaAPI(page, orgId, doomed.id)

    // The list still carries it — never hidden, or its owner could not find it
    // in order to fix it.
    await page.goto('/views')
    await expect(page.getByTestId('views-list')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(page)
    const row = page.getByTestId('view-row').filter({ hasText: viewName })
    await expect(row).toHaveCount(1)
    await expect(row).toHaveAttribute('data-valid', 'false')
    await expect(row.getByTestId('view-scope-chip')).toBeVisible()
    await expect(row.getByTestId('view-invalid-reason')).toContainText(
      'every space this view is scoped to has been deleted',
    )

    // And it still OPENS.
    await page.goto(`/views/${view.id}`)
    await expect(page.getByTestId('view-scope-unavailable')).toBeVisible({ timeout: 15000 })
    await expect(page.getByRole('heading', { name: viewName })).toBeVisible()
    await expect(page.getByText('Scope unavailable').first()).toBeVisible()

    // It must not read as a failure: no danger panel from either the view load
    // or the results, and no error string anywhere on the page.
    await expect(page.getByTestId('view-error')).toHaveCount(0)
    await expect(page.getByTestId('view-results-error')).toHaveCount(0)
    await assertNoErrors(page)

    // The owner is offered the fix rather than the delete.
    await page.getByRole('link', { name: 'Re-scope this view' }).click()
    await expect(page).toHaveURL(new RegExp(`/views/${view.id}/edit$`), { timeout: 15000 })
    await expect(page.getByTestId('view-name')).toHaveValue(viewName)
    await assertNoErrors(page)
  })
})
