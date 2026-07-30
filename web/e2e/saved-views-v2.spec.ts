import { test, expect, type Page } from '@playwright/test'
import { assertNoErrors, createUserAndLogin, getAuthToken, getCurrentUser } from './helpers/setup'

// Filter document v2 — date ranges and per-field negation — end to end.
//
// The journey the phase is scoped to: build "updated in the last 7 days, not
// closed", confirm the rows are right, save it as a view, and put a queue on
// it. Three surfaces evaluate the same document, and the point of doing it end
// to end is that only the first of them is the one v2 was written against — the
// queue and the gadget are supposed to inherit it for nothing, and "for
// nothing" is a claim worth checking rather than asserting.
//
// SEEDING CANNOT BACKDATE. Everything created through the API is updated_at =
// now, so a one-sided "last 7 days" filter would return every seeded row and
// pass with the predicate deleted. Both directions are therefore asserted from
// the same fresh fixtures: `after: -7d` must return them and `before: -7d` must
// return none of them. A dropped date predicate fails the second half
// immediately.
//
// Locator discipline follows saved-views.spec.ts: every E2E user lands in ONE
// shared org that accumulates rows across runs, so every assertion is scoped by
// a per-run token embedded in the seeded titles and never by a bare count.

function runToken(): string {
  return `v2${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
}

async function jsonHeaders(page: Page): Promise<Record<string, string>> {
  return {
    Authorization: `Bearer ${await getAuthToken(page)}`,
    'Content-Type': 'application/json',
  }
}

async function createSpace(page: Page, orgId: string, name: string): Promise<string> {
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
  const uniqueName = `${name} ${stamp}`
  const slug = uniqueName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  const key = ('V2' + Math.random().toString(36).replace(/[^a-z0-9]/g, '').toUpperCase()).slice(0, 8)
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces`, {
    headers: await jsonHeaders(page),
    data: { name: uniqueName, slug, key, type: 'beacon', visibility: 'org' },
  })
  if (res.status() !== 201) throw new Error(`create space: ${res.status()} ${await res.text()}`)
  return ((await res.json()) as { id: string }).id
}

async function createTicket(page: Page, orgId: string, spaceId: string, title: string): Promise<string> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/tickets`, {
    headers: await jsonHeaders(page),
    data: { title, priority: 'medium' },
  })
  if (res.status() !== 201) throw new Error(`create ticket: ${res.status()} ${await res.text()}`)
  return ((await res.json()) as { id: string }).id
}

async function setStatus(page: Page, orgId: string, spaceId: string, ticketId: string, status: string): Promise<void> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/tickets/${ticketId}/status`, {
    headers: await jsonHeaders(page),
    data: { status },
  })
  if (res.status() !== 200) throw new Error(`transition to ${status}: ${res.status()} ${await res.text()}`)
}

interface QueryDocLike {
  v: number
  filter: Record<string, unknown>
  sort: { field: string; dir: string }
}

/**
 * The stored document, declaring the LOWEST version it needs — which is what
 * the client does, so the E2E exercises the real shape rather than a
 * hand-stamped one. A filter using nothing v2 added stays v1.
 */
function queryDoc(filter: Record<string, unknown>): QueryDocLike {
  const usesV2 =
    ['created_at', 'updated_at', 'due_at', 'resolved_at'].some((f) => filter[f] !== undefined) ||
    Object.values((filter.not ?? {}) as Record<string, boolean>).some(Boolean)
  return { v: usesV2 ? 2 : 1, filter, sort: { field: 'updated_at', dir: 'desc' } }
}

async function createView(
  page: Page,
  orgId: string,
  name: string,
  query: QueryDocLike,
): Promise<string> {
  const res = await page.request.post(`/api/v1/orgs/${orgId}/views`, {
    headers: await jsonHeaders(page),
    data: { name, description: '', query, visibility: 'private', visibility_team_id: null },
  })
  if (res.status() !== 201) throw new Error(`create view: ${res.status()} ${await res.text()}`)
  return ((await res.json()) as { id: string }).id
}

async function viewTitles(page: Page, orgId: string, viewId: string): Promise<string[]> {
  const res = await page.request.get(`/api/v1/orgs/${orgId}/views/${viewId}/results`, {
    headers: await jsonHeaders(page),
  })
  if (res.status() !== 200) throw new Error(`view results: ${res.status()} ${await res.text()}`)
  const body = (await res.json()) as { results: Array<{ title: string }> | null }
  return (body.results ?? []).map((r) => r.title)
}

test.describe('filter v2 — dates and negation', () => {
  test('a "updated last 7 days, not closed" view returns the right rows, and a queue on it agrees', async ({
    page,
  }) => {
    const token = runToken()
    await createUserAndLogin(page)
    const me = await getCurrentUser(page)
    const orgId = me.orgId

    const spaceId = await createSpace(page, orgId, `Filter v2 ${token}`)
    const openTitle = `${token} still open`
    const closedTitle = `${token} already closed`
    await createTicket(page, orgId, spaceId, openTitle)
    const closedId = await createTicket(page, orgId, spaceId, closedTitle)
    await setStatus(page, orgId, spaceId, closedId, 'closed')

    // ── The headline document ────────────────────────────────────────────
    const recentNotClosed = await createView(
      page,
      orgId,
      `${token} recent not closed`,
      queryDoc({
        modules: ['beacon'],
        space_ids: [spaceId],
        text: token,
        updated_at: { after: '-7d' },
        statuses: ['closed'],
        not: { statuses: true },
      }),
    )
    const got = await viewTitles(page, orgId, recentNotClosed)
    expect(got).toContain(openTitle)
    expect(got).not.toContain(closedTitle)

    // ── The negation is doing the work ───────────────────────────────────
    // The identical document WITHOUT the negation returns the mirror image.
    // Without this, a filter that always returned one row would satisfy the
    // assertions above.
    const onlyClosed = await createView(
      page,
      orgId,
      `${token} only closed`,
      queryDoc({
        modules: ['beacon'],
        space_ids: [spaceId],
        text: token,
        updated_at: { after: '-7d' },
        statuses: ['closed'],
      }),
    )
    const closedOnly = await viewTitles(page, orgId, onlyClosed)
    expect(closedOnly).toContain(closedTitle)
    expect(closedOnly).not.toContain(openTitle)

    // ── The date bound is doing the work ─────────────────────────────────
    // Nothing seeded here can be older than this run, so a window that ENDS
    // seven days ago must contain none of it. If the date predicate were
    // dropped, this view would return both rows.
    const longAgo = await createView(
      page,
      orgId,
      `${token} before last week`,
      queryDoc({
        modules: ['beacon'],
        space_ids: [spaceId],
        text: token,
        updated_at: { before: '-7d' },
      }),
    )
    const stale = await viewTitles(page, orgId, longAgo)
    expect(stale).not.toContain(openTitle)
    expect(stale).not.toContain(closedTitle)

    // ── A queue inherits v2 for nothing ──────────────────────────────────
    // A queue is a space-bound saved view evaluated through the same document.
    // It was not changed for v2 and is not supposed to have been.
    const queueRes = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/queues`, {
      headers: await jsonHeaders(page),
      data: {
        name: `${token} queue`,
        description: '',
        query: queryDoc({
          modules: ['beacon'],
          text: token,
          updated_at: { after: '-7d' },
          statuses: ['closed'],
          not: { statuses: true },
        }),
      },
    })
    expect(queueRes.status(), await queueRes.text()).toBe(201)
    const queueId = ((await queueRes.json()) as { id: string }).id

    const queueResults = await page.request.get(
      `/api/v1/orgs/${orgId}/spaces/${spaceId}/queues/${queueId}/results`,
      { headers: await jsonHeaders(page) },
    )
    expect(queueResults.status(), await queueResults.text()).toBe(200)
    const queueBody = (await queueResults.json()) as { results: Array<{ title: string }> | null }
    const queueTitles = (queueBody.results ?? []).map((r) => r.title)
    expect(queueTitles).toContain(openTitle)
    expect(queueTitles).not.toContain(closedTitle)

    // ── A gadget's count agrees with the list it counts ──────────────────
    // The count and breakdown fan-outs are separate SQL from the list. A v2
    // term added to one and missed in the other reports a number for a query
    // nobody ran.
    const agg = await page.request.post(`/api/v1/orgs/${orgId}/views/aggregate`, {
      headers: await jsonHeaders(page),
      data: {
        query: queryDoc({
          modules: ['beacon'],
          space_ids: [spaceId],
          text: token,
          updated_at: { after: '-7d' },
          statuses: ['closed'],
          not: { statuses: true },
        }),
        group_by: 'status',
      },
    })
    expect(agg.status(), await agg.text()).toBe(200)
    const aggBody = (await agg.json()) as { total: number; buckets: Array<{ count: number }> | null }
    expect(aggBody.total).toBe(got.length)
    const summed = (aggBody.buckets ?? []).reduce((n, b) => n + b.count, 0)
    expect(summed).toBe(aggBody.total)

    await assertNoErrors(page)
  })

  test('the builder stores a relative period as a token and saves it', async ({ page }) => {
    const token = runToken()
    await createUserAndLogin(page)
    const me = await getCurrentUser(page)
    const orgId = me.orgId
    const spaceId = await createSpace(page, orgId, `Builder v2 ${token}`)
    await createTicket(page, orgId, spaceId, `${token} a ticket`)

    await page.goto('/views/new')
    await expect(page.getByTestId('query-filter-builder')).toBeVisible({ timeout: 15000 })

    // Choose a relative period through the real control.
    await page.getByTestId('view-date-updated_at').selectOption('last-7d')

    // The exclusion toggle is inert until its field names something — a control
    // that could build a document the server refuses is a control that lies.
    await expect(page.getByTestId('view-exclude-statuses')).toBeDisabled()
    await page.getByTestId('view-status-input').fill('closed')
    await page.getByTestId('view-status-add').click()
    await expect(page.getByTestId('view-exclude-statuses')).toBeEnabled()
    await page.getByTestId('view-exclude-statuses').click()

    await page.getByTestId('view-text').fill(token)
    await page.getByTestId('view-name').fill(`${token} from the builder`)

    // Waiting on the button going disabled would be wrong — it disables while
    // the request is in flight too. Wait on the response.
    const created = page.waitForResponse(
      (r) => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/views'),
    )
    await page.getByTestId('save-view').click()
    expect((await created).status()).toBe(201)

    await expect(page).toHaveURL(/\/views\/[0-9a-f-]{36}$/, { timeout: 15000 })
    const viewId = new URL(page.url()).pathname.split('/').pop() as string

    // What was STORED must be the token, not the instant it meant when the
    // person clicked. An ISO timestamp here would look right today and stop
    // meaning "the last 7 days" tomorrow.
    const res = await page.request.get(`/api/v1/orgs/${orgId}/views/${viewId}`, {
      headers: await jsonHeaders(page),
    })
    expect(res.status()).toBe(200)
    const stored = (await res.json()) as { query: QueryDocLike }
    expect(stored.query.v).toBe(2)
    expect(stored.query.filter.updated_at).toEqual({ after: '-7d' })
    expect(stored.query.filter.not).toEqual({ statuses: true })

    await assertNoErrors(page)
  })
})
