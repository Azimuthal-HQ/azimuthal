import { test, expect, type Page } from '@playwright/test'
import { assertNoErrors, createUserAndLogin, getAuthToken, getCurrentUser } from './helpers/setup'

// P6 cross-module search — the journey the surface exists for.
//
// The point of doing this end to end rather than in vitest is the CROSSING: a
// ticket and a page, seeded through two different modules' APIs, coming back in
// ONE ranked list from one box. Every layer between the generated tsvector and
// the rendered row has to agree for that to happen, and each of them is mocked
// away in the unit tests.

async function jsonHeaders(page: Page): Promise<Record<string, string>> {
  return {
    Authorization: `Bearer ${await getAuthToken(page)}`,
    'Content-Type': 'application/json',
  }
}

async function createSpace(page: Page, orgId: string, type: string, name: string): Promise<string> {
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
  const uniqueName = `${name} ${stamp}`
  const slug = uniqueName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  const key = ('SR' + Math.random().toString(36).replace(/[^a-z0-9]/g, '').toUpperCase()).slice(0, 8)
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces`, {
    headers: await jsonHeaders(page),
    data: { name: uniqueName, slug, key, type },
  })
  if (res.status() !== 201) throw new Error(`create ${type} space: ${res.status()} ${await res.text()}`)
  return ((await res.json()) as { id: string }).id
}

test.describe('Cross-module search', () => {
  test('one query returns a ticket and a page together, and each opens', async ({ page }) => {
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)

    // A term that cannot collide with fixture noise from other specs sharing
    // this database — "ticket" or "page" would match half the corpus.
    const term = `zarquon${Math.random().toString(36).slice(2, 7)}`

    const beacon = await createSpace(page, orgId, 'beacon', 'Search Beacon')
    const codex = await createSpace(page, orgId, 'codex', 'Search Codex')

    const ticketRes = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${beacon}/tickets`, {
      headers: await jsonHeaders(page),
      data: { title: `${term} outage`, priority: 'medium' },
    })
    expect(ticketRes.status()).toBe(201)

    const pageRes = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${codex}/wiki`, {
      headers: await jsonHeaders(page),
      data: { title: `${term} runbook`, content: 'how to recover' },
    })
    expect(pageRes.status()).toBe(201)

    // Through the top bar, not by typing the URL: the launcher being wired is
    // half of what this journey is checking.
    await page.goto('/')
    await page.getByTestId('search-launcher').click()
    await page.getByTestId('search-launcher-input').fill(term)

    const suggestions = page.getByTestId('search-launcher-result')
    await expect(suggestions).toHaveCount(2)

    // Enter opens the full surface with the query in the URL, so the search is
    // linkable rather than trapped in component state.
    await page.getByTestId('search-launcher-input').press('Enter')
    await expect(page).toHaveURL(new RegExp(`/search\\?q=${term}`))

    const results = page.getByTestId('search-result')
    await expect(results).toHaveCount(2)
    // BOTH modules, which is the crossing. A regression that dropped one
    // fan-out would still show a plausible single result.
    await expect(page.locator('[data-testid="search-result"][data-module="beacon"]')).toHaveCount(1)
    await expect(page.locator('[data-testid="search-result"][data-module="codex"]')).toHaveCount(1)

    // The row navigates into the entity it names.
    await page.locator('[data-testid="search-result"][data-module="codex"]').click()
    await expect(page).toHaveURL(new RegExp(`/codex/${codex}/pages/`))
    await expect(page.getByText(`${term} runbook`).first()).toBeVisible()

    await assertNoErrors(page)
  })

  test('type: narrows the fan-out, and a stopword query says so', async ({ page }) => {
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)
    const term = `zarquon${Math.random().toString(36).slice(2, 7)}`

    const beacon = await createSpace(page, orgId, 'beacon', 'Search Beacon')
    const codex = await createSpace(page, orgId, 'codex', 'Search Codex')
    await page.request.post(`/api/v1/orgs/${orgId}/spaces/${beacon}/tickets`, {
      headers: await jsonHeaders(page),
      data: { title: `${term} outage`, priority: 'medium' },
    })
    await page.request.post(`/api/v1/orgs/${orgId}/spaces/${codex}/wiki`, {
      headers: await jsonHeaders(page),
      data: { title: `${term} runbook`, content: 'how to recover' },
    })

    // Premise: unnarrowed, both come back. Without it the narrowed assertion
    // below would pass against a search that had stopped working entirely.
    await page.goto(`/search?q=${term}`)
    await expect(page.getByTestId('search-result')).toHaveCount(2)

    await page.goto(`/search?q=${encodeURIComponent(`type:ticket ${term}`)}`)
    await expect(page.getByTestId('search-result')).toHaveCount(1)
    await expect(page.locator('[data-testid="search-result"][data-module="beacon"]')).toHaveCount(1)

    // A query of nothing but stopwords is NOT "no results" — it is its own
    // answer, and rendering it as an ordinary empty list is the failure this
    // asserts against.
    await page.goto('/search?q=the%20of%20a')
    await expect(page.getByText('No searchable terms')).toBeVisible()
    await expect(page.getByTestId('search-result')).toHaveCount(0)

    await assertNoErrors(page)
  })

  test('a tag set on a ticket through its detail page is findable with tag:', async ({ page }) => {
    // The entity-tags convergence, end to end: tags stopped being page-only,
    // so the whole journey — the editor on a TICKET detail page, the
    // association write, the Beacon search arm's tag filter, the cross-module
    // browse — has to hold together for a kind that had no tag surface at all
    // before migration 055.
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)

    const term = `fenchur${Math.random().toString(36).slice(2, 7)}`
    const tag = `etag${Math.random().toString(36).slice(2, 7)}`
    const beacon = await createSpace(page, orgId, 'beacon', 'Tagged Beacon')

    const taggedRes = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${beacon}/tickets`, {
      headers: await jsonHeaders(page),
      data: { title: `${term} incident`, priority: 'medium' },
    })
    expect(taggedRes.status()).toBe(201)
    const taggedId = ((await taggedRes.json()) as { id: string }).id
    // A second ticket matching the TEXT but never tagged — the row that proves
    // tag: filters rather than merely fanning out.
    const bystanderRes = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${beacon}/tickets`, {
      headers: await jsonHeaders(page),
      data: { title: `${term} bystander`, priority: 'medium' },
    })
    expect(bystanderRes.status()).toBe(201)

    // Tag the first ticket through its own detail surface, not the API: the
    // editor being wired on a ticket is half of what this journey checks.
    await page.goto(`/beacon/${beacon}/tickets/${taggedId}`)
    await page.getByTestId('codex-tag-input').fill(tag)
    await page.getByTestId('codex-tag-input').press('Enter')
    await expect(page.getByTestId('codex-page-tag')).toHaveText(tag, { timeout: 10000 })

    // tag: narrows to entities CARRYING the tag — one hit, the ticket, found
    // through Beacon's own search arm.
    await page.goto(`/search?q=${encodeURIComponent(`tag:${tag} ${term}`)}`)
    await expect(page.getByTestId('search-result')).toHaveCount(1)
    await expect(page.locator('[data-testid="search-result"][data-module="beacon"]')).toHaveCount(1)
    await expect(page.getByTestId('search-result')).toContainText(`${term} incident`)

    // And the chip's browse lists the ticket under the tag, cross-module.
    await page.goto(`/beacon/${beacon}/tags/${tag}`)
    const row = page.getByTestId('codex-tag-page-row')
    await expect(row).toHaveCount(1)
    await expect(row).toHaveAttribute('data-entity-type', 'ticket')
    await expect(row).toContainText(`${term} incident`)

    await assertNoErrors(page)
  })

  test('the tag browse opens a project item at the id its detail route resolves', async ({
    page,
  }) => {
    // The item arm of the convergence — and the regression the browse's item
    // link carried. Rows linked items by their human key (VEC-14), but the
    // detail route parses its segment as a UUID, so the item row landed on "The
    // item could not be loaded." The ticket-only browse leg above asserted the
    // row's attributes but never FOLLOWED it, which is exactly how this
    // survived; here the item row is followed all the way to a loaded page.
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)

    const term = `zaphod${Math.random().toString(36).slice(2, 7)}`
    const tag = `itag${Math.random().toString(36).slice(2, 7)}`
    const vector = await createSpace(page, orgId, 'vector', 'Tagged Vector')

    // An item, created and tagged through the API. Its human key is composed
    // server-side and is NOT its id — the distinction the browse link must
    // respect, and the only reason this test can tell a correct link from a
    // coincidence.
    const itemRes = await page.request.post(
      `/api/v1/orgs/${orgId}/spaces/${vector}/projects/items`,
      {
        headers: await jsonHeaders(page),
        data: { title: `${term} rewrite`, kind: 'task', priority: 'medium' },
      },
    )
    expect(itemRes.status()).toBe(201)
    const itemId = ((await itemRes.json()) as { id: string }).id

    const tagRes = await page.request.put(
      `/api/v1/orgs/${orgId}/spaces/${vector}/projects/items/${itemId}/tags`,
      { headers: await jsonHeaders(page), data: { tags: [tag] } },
    )
    expect(tagRes.ok(), `tagging the item failed: ${tagRes.status()}`).toBeTruthy()

    // The browse lists the item under the tag. The label is unique to this run,
    // so the count is exact rather than a "contains" over the org.
    await page.goto(`/vector/${vector}/tags/${tag}`)
    const row = page.getByTestId('codex-tag-page-row')
    await expect(row).toHaveCount(1)
    await expect(row).toHaveAttribute('data-entity-type', 'project_item')
    // The href pins the id, not the key: a regression to key-linking would end
    // this href with the human ref and 400 the moment it was followed.
    await expect(row).toHaveAttribute('href', `/vector/${vector}/backlog/${itemId}`)

    // Follow it — and the detail page LOADS. This is the assertion the
    // ticket-only leg could not make: assertNoErrors catches the "could not be
    // loaded" friendly-error the broken link produced, and the visible title
    // is positive proof the item resolved.
    await row.click()
    await expect(page).toHaveURL(new RegExp(`/vector/${vector}/backlog/${itemId}$`), {
      timeout: 10000,
    })
    await expect(page.getByRole('heading', { name: `${term} rewrite` })).toBeVisible({
      timeout: 10000,
    })
    await assertNoErrors(page)
  })
})
