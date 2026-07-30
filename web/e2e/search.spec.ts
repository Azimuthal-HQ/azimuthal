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
})
