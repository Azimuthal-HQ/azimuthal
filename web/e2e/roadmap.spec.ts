import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors, getAuthToken, getCurrentUser } from './helpers/setup'

// W3: the roadmap's Sprint Timeline is a real chart — sprints as spans on a
// shared axis, items placed against their sprint's span. Part of the E2E
// backfill owed from #67.

test.describe('Roadmap', () => {
  test('sprint timeline renders a span per dated sprint, with its items', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Roadmap Timeline', 'vector')
    const token = await getAuthToken(page)
    const { orgId } = await getCurrentUser(page)
    const base = `/api/v1/orgs/${orgId}/spaces/${spaceId}/projects`
    const headers = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }

    // Seed through the API: the UI paths for creating dated sprints and
    // assigning items are covered by sprints.spec.ts; this test is about
    // rendering, so it sets up state directly.
    const sprintRes = await page.request.post(`${base}/sprints`, {
      headers,
      data: {
        name: 'Timeline Sprint',
        starts_at: '2026-07-06T00:00:00Z',
        ends_at: '2026-07-20T00:00:00Z',
      },
    })
    expect(sprintRes.status()).toBe(201)
    const sprint = await sprintRes.json() as { id: string }

    const itemRes = await page.request.post(`${base}/items`, {
      headers,
      data: { title: 'Timeline Item', kind: 'task', priority: 'medium' },
    })
    expect(itemRes.status()).toBe(201)
    const item = await itemRes.json() as { id: string }

    const assignRes = await page.request.post(`${base}/items/${item.id}/sprint`, {
      headers,
      data: { sprint_id: sprint.id },
    })
    expect(assignRes.status()).toBe(200)

    await page.goto(`/vector/${spaceId}/roadmap`)
    await expect(page.getByRole('heading', { level: 1, name: 'Roadmap' })).toBeVisible({ timeout: 10000 })

    await page.getByRole('radio', { name: 'Sprint Timeline' }).click()

    // The chart, not just a list of cards.
    const timeline = page.getByTestId('sprint-timeline')
    await expect(timeline).toBeVisible({ timeout: 10000 })
    await expect(timeline.getByTestId('timeline-bar').first()).toBeVisible()

    // The sprint's items are placed against its span once the row expands.
    await timeline.getByRole('button', { name: /Toggle items for Timeline Sprint/i }).click()
    await expect(timeline.getByText('Timeline Item')).toBeVisible()

    // Item keys ride along as provenance chips.
    await expect(timeline.getByTestId('item-key-chip').first()).toBeVisible()

    // The range control is present and switching it keeps the chart rendered.
    await page.getByRole('radio', { name: '12 months' }).click()
    await expect(timeline.getByTestId('timeline-bar').first()).toBeVisible()

    await assertNoErrors(page)
  })

  test('empty roadmap shows the empty state, not an error', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Roadmap Empty', 'vector')

    await page.goto(`/vector/${spaceId}/roadmap`)
    await expect(page.getByRole('heading', { level: 1, name: 'Roadmap' })).toBeVisible({ timeout: 10000 })

    await expect(page.getByText(/No items with due dates/i)).toBeVisible()
    // #64's contract: empty is not an error.
    await expect(page.getByTestId('roadmap-error')).not.toBeVisible()

    await page.getByRole('radio', { name: 'Sprint Timeline' }).click()
    await expect(page.getByText(/No sprints with items found/i)).toBeVisible()
    await expect(page.getByTestId('roadmap-error')).not.toBeVisible()
    await assertNoErrors(page)
  })

  test('a failing roadmap request renders the error state, distinct from empty', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Roadmap Error', 'vector')

    // #64's other half: an API failure must not read as "nothing scheduled".
    await page.route('**/projects/roadmap?**', route =>
      route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"boom"}' }),
    )

    await page.goto(`/vector/${spaceId}/roadmap`)
    await expect(page.getByTestId('roadmap-error')).toBeVisible({ timeout: 15000 })
    await expect(page.getByText(/No items with due dates/i)).not.toBeVisible()
  })
})
