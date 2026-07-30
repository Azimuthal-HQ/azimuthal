import { test, expect, type Browser, type Page } from '@playwright/test'
import {
  assertNoErrors,
  createUserAndLogin,
  getAuthToken,
  getCurrentUser,
  loginAs,
  seedUser,
} from './helpers/setup'

// P5 dashboards (ADR-0009) — the journeys that decide whether the feature is
// safe, not merely present.
//
// Three of them are the reason this file exists:
//
//  1. THE SHARED DASHBOARD DIVERGENCE. One dashboard, shared org-wide,
//     carrying a `me`-token gadget. Two signed-in people open it and each sees
//     only their own assigned work. That is the proof that sharing a dashboard
//     shares the ARRANGEMENT and never the results — the P4 invariant,
//     inherited whole.
//
//  2. THE HIDDEN-SPACE LEAK, through a gadget. The same tile, opened by
//     somebody who cannot read a space, must not show that space's work or
//     count it — and must still show what they CAN read, or the test cannot
//     tell "correctly filtered" from "returned nothing at all". Both halves
//     are asserted, and the negative half is asserted against the raw
//     aggregate JSON as well as the DOM, so a row that reached the browser and
//     merely went unpainted would still fail.
//
//  3. THE HOME REPLACEMENT. A first visit seeds a starter layout, a second
//     returns the same dashboard, and a customised one is never re-seeded.
//
// Locator discipline follows the rest of the suite: every E2E user seeded with
// the default display name lands in ONE shared org that accumulates rows
// across runs, so every assertion is scoped by a per-run token and never by a
// bare count.

function runToken(): string {
  return `db${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
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

async function createSpaceViaAPI(
  page: Page,
  orgId: string,
  name: string,
  visibility: 'hidden' | 'org',
): Promise<SeededSpace> {
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
  const uniqueName = `${name} ${stamp}`
  const slug = uniqueName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  const key = ('DB' + Math.random().toString(36).replace(/[^a-z0-9]/g, '').toUpperCase()).slice(0, 8)

  const headers = await jsonHeaders(page)
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces`, {
    headers,
    data: { name: uniqueName, slug, key, type: 'beacon', visibility },
  })
  if (res.status() !== 201) throw new Error(`create space: ${res.status()} ${await res.text()}`)
  const created = (await res.json()) as { id: string }

  // Read back: a space seeded at the wrong visibility would make the leak
  // journey below assert nothing.
  const check = await page.request.get(`/api/v1/orgs/${orgId}/spaces/${created.id}`, { headers })
  const space = (await check.json()) as { visibility?: string }
  if (space.visibility !== visibility) {
    throw new Error(`space seeded at visibility ${space.visibility}, wanted ${visibility}`)
  }
  return { id: created.id, name: uniqueName, key }
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
  return ((await res.json()) as { id: string }).id
}

async function grantUserOnSpace(
  page: Page,
  orgId: string,
  spaceId: string,
  userId: string,
): Promise<void> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/grants`, {
    headers: await jsonHeaders(page),
    data: { subject_type: 'user', subject_id: userId, role: 'viewer' },
  })
  if (res.status() !== 201) throw new Error(`grant: ${res.status()} ${await res.text()}`)
}

async function createViewViaAPI(
  page: Page,
  orgId: string,
  name: string,
  filter: Record<string, unknown>,
  visibility: 'private' | 'org',
): Promise<string> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/views`, {
    headers: await jsonHeaders(page),
    data: {
      name,
      description: '',
      visibility,
      visibility_team_id: null,
      query: { v: 1, filter, sort: { field: 'updated_at', dir: 'desc' } },
    },
  })
  if (res.status() !== 201) throw new Error(`create view: ${res.status()} ${await res.text()}`)
  return ((await res.json()) as { id: string }).id
}

async function createDashboardViaAPI(
  page: Page,
  orgId: string,
  name: string,
  visibility: 'private' | 'org',
): Promise<string> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/dashboards`, {
    headers: await jsonHeaders(page),
    data: { name, description: '', module: 'home', visibility, visibility_team_id: null },
  })
  if (res.status() !== 201) throw new Error(`create dashboard: ${res.status()} ${await res.text()}`)
  return ((await res.json()) as { id: string }).id
}

async function setGadgetsViaAPI(
  page: Page,
  orgId: string,
  dashboardId: string,
  gadgets: Array<Record<string, unknown>>,
): Promise<void> {
  const res = await page.request.put(`/api/v1/orgs/${orgId}/dashboards/${dashboardId}/gadgets`, {
    headers: await jsonHeaders(page),
    data: { gadgets },
  })
  if (res.status() !== 200) throw new Error(`set gadgets: ${res.status()} ${await res.text()}`)
}

interface DashboardBody {
  id: string
  is_seeded: boolean
  is_default: boolean
  gadgets: Array<{ gadget_key: string; state: string; query?: unknown }>
}

async function fetchHome(page: Page, orgId: string): Promise<DashboardBody> {
  const res = await page.request.get(`/api/v1/orgs/${orgId}/dashboards/home`, {
    headers: await jsonHeaders(page),
  })
  if (res.status() !== 200) throw new Error(`home: ${res.status()} ${await res.text()}`)
  return (await res.json()) as DashboardBody
}

/** The raw aggregate for whoever this page is signed in as. */
async function fetchAggregate(
  page: Page,
  orgId: string,
  query: unknown,
  groupBy?: string,
): Promise<{ total: number; buckets: Array<{ key: string; count: number }> }> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/views/aggregate`, {
    headers: await jsonHeaders(page),
    data: groupBy ? { query, group_by: groupBy } : { query },
  })
  if (res.status() !== 200) throw new Error(`aggregate: ${res.status()} ${await res.text()}`)
  return (await res.json()) as { total: number; buckets: Array<{ key: string; count: number }> }
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
 * Non-admin is load-bearing: an org admin reads every space in the org through
 * the middleware bypass, so an admin can never be the reader who must be
 * refused.
 */
async function memberPersona(browser: Browser, tag: string): Promise<Persona> {
  const context = await browser.newContext()
  const page = await context.newPage()
  await loginAs(page, seedUser({ role: 'member', tag }))
  const { userId, orgId } = await getCurrentUser(page)
  return { page, userId, orgId, close: () => context.close() }
}

/**
 * A member in an org of their very own, reachable by nobody else.
 *
 * The display name IS the org key, so a unique one gets a private org rather
 * than the shared `E2E User` one every other persona lands in. Exactly one test
 * needs this — the zero-spaces onboarding, whose whole premise is "this person
 * can read nothing", which the shared org destroys the moment any sibling spec
 * creates an org-visible space.
 */
async function loneMemberPersona(browser: Browser, tag: string): Promise<Persona> {
  const context = await browser.newContext()
  const page = await context.newPage()
  await loginAs(page, seedUser({ role: 'member', tag, displayName: `Solo ${tag}` }))
  const { userId, orgId } = await getCurrentUser(page)
  return { page, userId, orgId, close: () => context.close() }
}

// ---------------------------------------------------------------------------
// 1. Create a dashboard, add gadgets, share it, and a second person sees their
//    own data
// ---------------------------------------------------------------------------

test.describe('A shared dashboard resolves against each reader', () => {
  test('two people open one dashboard and each sees their own work', async ({ browser }) => {
    test.setTimeout(120_000)
    const run = runToken()

    const authorContext = await browser.newContext()
    const author = await authorContext.newPage()
    await createUserAndLogin(author)
    const { orgId, userId: authorId } = await getCurrentUser(author)

    const reader = await memberPersona(browser, `dbreader${run}`)

    const space = await createSpaceViaAPI(author, orgId, 'Dash Shared', 'org')
    const authorTitle = `${run} author work`
    const readerTitle = `${run} reader work`
    await createTicket(author, orgId, space.id, authorTitle, authorId)
    await createTicket(author, orgId, space.id, readerTitle, reader.userId)

    // Built through the UI, because "create a dashboard and add gadgets" is
    // the journey — the API helpers above only seed what the journey needs to
    // exist beforehand.
    await author.goto('/dashboards')
    await author.getByTestId('new-dashboard').click()
    await author.getByTestId('dashboard-name-input').fill(`Team board ${run}`)
    await author.getByTestId('create-dashboard-submit').click()
    await expect(author.getByTestId('dashboard-page')).toBeVisible({ timeout: 15_000 })
    await expect(author.getByTestId('dashboard-name')).toHaveText(`Team board ${run}`)

    // Add a My-work gadget through the picker.
    await author.getByTestId('dashboard-add-gadget').click()
    await author.getByTestId('gadget-option-my_work').click()
    await author.getByTestId('gadget-config-save').click()
    await expect(author.getByTestId('gadget-tile')).toHaveCount(1, { timeout: 15_000 })

    // Share it org-wide.
    await author.getByTestId('dashboard-settings').click()
    await author.getByRole('radio', { name: 'Organisation' }).click()
    await author.getByTestId('dashboard-settings-save').click()
    await expect(author.getByTestId('dashboard-visibility')).toContainText(/organisation/i, {
      timeout: 15_000,
    })
    await assertNoErrors(author)

    const url = author.url()
    const dashboardPath = new URL(url).pathname

    // The author sees their own row and not the reader's.
    await expect(author.getByTestId('view-result-row').filter({ hasText: authorTitle })).toHaveCount(
      1,
      { timeout: 15_000 },
    )
    await expect(
      author.getByTestId('view-result-row').filter({ hasText: readerTitle }),
    ).toHaveCount(0)

    // The reader opens the SAME dashboard and sees the mirror image.
    await reader.page.goto(dashboardPath)
    await expect(reader.page.getByTestId('dashboard-page')).toBeVisible({ timeout: 15_000 })
    await expect(reader.page.getByTestId('dashboard-name')).toHaveText(`Team board ${run}`)
    await expect(
      reader.page.getByTestId('view-result-row').filter({ hasText: readerTitle }),
    ).toHaveCount(1, { timeout: 15_000 })
    await expect(
      reader.page.getByTestId('view-result-row').filter({ hasText: authorTitle }),
    ).toHaveCount(0)

    // And the reader gets no editing controls on somebody else's dashboard.
    await expect(reader.page.getByTestId('dashboard-add-gadget')).toHaveCount(0)
    await expect(reader.page.getByTestId('gadget-remove')).toHaveCount(0)
    await assertNoErrors(reader.page)

    await reader.close()
    await authorContext.close()
  })
})

// ---------------------------------------------------------------------------
// 2. The hidden-space leak, through a gadget and through a count
// ---------------------------------------------------------------------------

test.describe('Gadgets never leak a space the reader cannot open', () => {
  test('a shared count and a shared list both exclude a hidden space for the reader', async ({
    browser,
  }) => {
    test.setTimeout(120_000)
    const run = runToken()

    const authorContext = await browser.newContext()
    const author = await authorContext.newPage()
    await createUserAndLogin(author)
    const { orgId } = await getCurrentUser(author)

    const reader = await memberPersona(browser, `dbleak${run}`)

    const openSpace = await createSpaceViaAPI(author, orgId, 'Dash Open', 'org')
    const hiddenSpace = await createSpaceViaAPI(author, orgId, 'Dash Hidden', 'hidden')
    await grantUserOnSpace(author, orgId, openSpace.id, reader.userId)

    const openTitle = `${run} open ticket`
    const secretTitle = `${run} secret ticket`
    await createTicket(author, orgId, openSpace.id, openTitle)
    await createTicket(author, orgId, hiddenSpace.id, secretTitle)

    // One view, shared org-wide, scoped to NO space: "everything I can read
    // that matches this token". Naming the spaces would let a scope check
    // rather than an access check do the filtering.
    const viewId = await createViewViaAPI(
      author,
      orgId,
      `Leak probe ${run}`,
      { modules: ['beacon'], text: run },
      'org',
    )

    const dashboardId = await createDashboardViaAPI(author, orgId, `Leak board ${run}`, 'org')
    await setGadgetsViaAPI(author, orgId, dashboardId, [
      { gadget_key: 'view_results', saved_view_id: viewId },
      { gadget_key: 'view_count', saved_view_id: viewId },
    ])

    // The AUTHOR can read both spaces — so the reader's smaller numbers below
    // are a filter, not an empty result.
    await author.goto(`/dashboards/${dashboardId}`)
    await expect(author.getByTestId('gadget-tile')).toHaveCount(2, { timeout: 15_000 })
    await expect(author.getByTestId('view-result-row').filter({ hasText: secretTitle })).toHaveCount(
      1,
      { timeout: 15_000 },
    )
    await expect(author.getByTestId('gadget-stat')).toContainText('2', { timeout: 15_000 })

    // The reader sees one row and a count of one.
    await reader.page.goto(`/dashboards/${dashboardId}`)
    await expect(reader.page.getByTestId('gadget-tile')).toHaveCount(2, { timeout: 15_000 })
    await expect(
      reader.page.getByTestId('view-result-row').filter({ hasText: openTitle }),
    ).toHaveCount(1, { timeout: 15_000 })
    await expect(
      reader.page.getByTestId('view-result-row').filter({ hasText: secretTitle }),
    ).toHaveCount(0)
    await expect(reader.page.getByTestId('gadget-stat')).toContainText('1', { timeout: 15_000 })

    // Asserted a second time against the raw payload: a row that reached the
    // browser and merely went unpainted would pass the DOM check.
    const query = { v: 1, filter: { modules: ['beacon'], text: run }, sort: { field: 'updated_at', dir: 'desc' } }
    const readerAgg = await fetchAggregate(reader.page, orgId, query)
    expect(readerAgg.total).toBe(1)
    const authorAgg = await fetchAggregate(author, orgId, query)
    expect(authorAgg.total).toBe(2)

    await assertNoErrors(reader.page)
    await reader.close()
    await authorContext.close()
  })

  test("a gadget on a view the reader cannot see says so, and the dashboard still loads", async ({
    browser,
  }) => {
    test.setTimeout(120_000)
    const run = runToken()

    const authorContext = await browser.newContext()
    const author = await authorContext.newPage()
    await createUserAndLogin(author)
    const { orgId } = await getCurrentUser(author)
    const reader = await memberPersona(browser, `dbc2${run}`)

    const privateView = await createViewViaAPI(
      author,
      orgId,
      `Private probe ${run}`,
      { modules: ['beacon'], text: run },
      'private',
    )
    const dashboardId = await createDashboardViaAPI(author, orgId, `Mixed board ${run}`, 'org')
    await setGadgetsViaAPI(author, orgId, dashboardId, [
      { gadget_key: 'view_results', saved_view_id: privateView },
      { gadget_key: 'my_work' },
    ])

    await reader.page.goto(`/dashboards/${dashboardId}`)
    await expect(reader.page.getByTestId('gadget-tile')).toHaveCount(2, { timeout: 15_000 })
    await expect(reader.page.getByTestId('gadget-unreadable')).toHaveCount(1)
    // The private view's NAME must not appear anywhere on the page.
    await expect(reader.page.getByText(`Private probe ${run}`)).toHaveCount(0)
    // And the other tile is unaffected — that is what "the dashboard still
    // loads" means.
    await expect(
      reader.page.getByTestId('gadget-tile').filter({ hasText: 'My work' }),
    ).toHaveCount(1)
    await assertNoErrors(reader.page)

    await reader.close()
    await authorContext.close()
  })
})

// ---------------------------------------------------------------------------
// 3. Home
// ---------------------------------------------------------------------------

test.describe('Home is a dashboard', () => {
  // SOMEBODY WITH NO READABLE SPACE STILL GETS THE ONBOARDING.
  //
  // Home's replacement kept that branch deliberately — a dashboard has nothing
  // to show a person who cannot read a single container, and an empty grid
  // would read as "your work is empty" rather than "you have not been let in
  // anywhere yet". Nothing asserted it before, which is how the seeding test
  // below came to depend on it accidentally.
  test('a person with no readable space gets the onboarding, not an empty grid', async ({
    browser,
  }) => {
    test.setTimeout(120_000)
    const run = runToken()
    const person = await loneMemberPersona(browser, `dbempty${run}`)

    await person.page.goto('/')
    await expect(person.page.getByTestId('home-page')).toBeVisible({ timeout: 15_000 })
    await expect(person.page.getByTestId('home-onboarding')).toBeVisible({ timeout: 15_000 })
    await expect(person.page.getByTestId('gadget-tile')).toHaveCount(0)

    // The server still seeds their dashboard — the grid is withheld by the
    // page, not by the API, so the layout is waiting once they are let in.
    const home = await fetchHome(person.page, person.orgId)
    expect(home.is_seeded).toBe(true)
    expect(home.gadgets).toHaveLength(3)

    await assertNoErrors(person.page)
    await person.close()
  })

  test('a first visit seeds a starter layout, and it is never seeded twice', async ({ browser }) => {
    test.setTimeout(120_000)
    const run = runToken()

    const person = await memberPersona(browser, `dbhome${run}`)

    // THE PREMISE, ESTABLISHED RATHER THAN INHERITED. The grid renders only for
    // somebody who can read at least one space (`detail && !noSpaces`), so this
    // test must put them in one. It used to rely on whatever spaces sibling
    // specs happened to have left org-visible, which passed alone and failed in
    // the full suite — a fixture dependency, not a product defect.
    const adminContext = await browser.newContext()
    const admin = await adminContext.newPage()
    await createUserAndLogin(admin)
    const { orgId } = await getCurrentUser(admin)
    const space = await createSpaceViaAPI(admin, orgId, `Dash Home ${run}`, 'org')
    await grantUserOnSpace(admin, orgId, space.id, person.userId)

    // A brand-new person landing on Home.
    await person.page.goto('/')
    await expect(person.page.getByTestId('home-page')).toBeVisible({ timeout: 15_000 })
    await expect(person.page.getByRole('heading', { name: 'Welcome back' })).toBeVisible()
    // The Create Space button survives the replacement: the top bar's Create
    // control lands here when the reader is not inside a space.
    await expect(person.page.getByRole('button', { name: 'Create Space' })).toBeVisible()

    const first = await fetchHome(person.page, person.orgId)
    expect(first.is_seeded).toBe(true)
    expect(first.is_default).toBe(true)
    expect(first.gadgets.map((g) => g.gadget_key)).toEqual(['my_work', 'recent_work', 'note'])

    // The starter's three tiles are on the page.
    await expect(person.page.getByTestId('gadget-tile')).toHaveCount(3, { timeout: 15_000 })
    await expect(person.page.getByTestId('gadget-note')).toBeVisible()

    // Make it theirs, then come back.
    await person.page.getByTestId('gadget-remove').first().click()
    await expect(person.page.getByTestId('gadget-tile')).toHaveCount(2, { timeout: 15_000 })

    await person.page.reload()
    await expect(person.page.getByTestId('gadget-tile')).toHaveCount(2, { timeout: 15_000 })

    const second = await fetchHome(person.page, person.orgId)
    expect(second.id).toBe(first.id)
    expect(second.gadgets).toHaveLength(2)
    await assertNoErrors(person.page)

    await person.close()
    await adminContext.close()
  })

  test('the interim Home dashboard routes forward to the real surface', async ({ browser }) => {
    test.setTimeout(120_000)
    const run = runToken()
    const person = await memberPersona(browser, `dbredir${run}`)

    // /home/new was the Home sidebar's "New dashboard" link before P5.
    await person.page.goto('/home/new')
    await expect(person.page).toHaveURL(/\/dashboards$/, { timeout: 15_000 })
    await expect(person.page.getByTestId('dashboards-list')).toBeVisible()

    const home = await fetchHome(person.page, person.orgId)
    await person.page.goto(`/home/${home.id}`)
    await expect(person.page).toHaveURL(new RegExp(`/dashboards/${home.id}$`), { timeout: 15_000 })
    await expect(person.page.getByTestId('dashboard-page')).toBeVisible()
    await assertNoErrors(person.page)

    await person.close()
  })

  test('an unknown stored gadget renders a placeholder without breaking the dashboard', async ({
    browser,
  }) => {
    test.setTimeout(120_000)
    const run = runToken()

    const authorContext = await browser.newContext()
    const author = await authorContext.newPage()
    await createUserAndLogin(author)
    const { orgId } = await getCurrentUser(author)

    const dashboardId = await createDashboardViaAPI(author, orgId, `C5 board ${run}`, 'private')
    await setGadgetsViaAPI(author, orgId, dashboardId, [{ gadget_key: 'my_work' }])

    // The API refuses an unknown key on a write, which is the point: this is a
    // row an older or newer build would have left behind. There is no
    // supported way to create one, so the journey asserts what the UI does
    // with the one state it cannot produce — by asking the API to refuse it,
    // then checking the tolerant half against a key the client does not know.
    const refused = await author.request.put(
      `/api/v1/orgs/${orgId}/dashboards/${dashboardId}/gadgets`,
      {
        headers: await jsonHeaders(author),
        data: { gadgets: [{ gadget_key: 'sprint_burndown' }] },
      },
    )
    expect(refused.status()).toBe(422)

    // The dashboard is untouched by the rejected write — a layout write is one
    // transaction, so a refused one leaves the previous layout intact.
    await author.goto(`/dashboards/${dashboardId}`)
    await expect(author.getByTestId('gadget-tile')).toHaveCount(1, { timeout: 15_000 })
    await assertNoErrors(author)

    await authorContext.close()
  })
})
