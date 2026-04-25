import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace } from './helpers/setup'

// Audit ref: testing-audit.md §7.5 — covers the v0.1.8 regression where
// list endpoints returning literal `null` caused .slice/.map to throw and
// blanked the page. We force each list endpoint to return null and assert
// the rendering page does not crash and shows no "Something went wrong"
// boundary.

test.describe('Null collection responses do not crash the UI', () => {
  test('dashboard renders when /spaces returns null', async ({ page }) => {
    await page.route('**/api/v1/orgs/*/spaces', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: 'null' }),
    )
    await createUserAndLogin(page)
    await expect(page).toHaveURL(/^.*\/(?!login).*$/)
    await expect(page.locator('text=Something went wrong')).not.toBeVisible()
  })

  test('ticket list renders when /tickets returns null', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Null Tickets', 'service_desk')

    await page.route('**/api/v1/spaces/*/tickets', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: 'null' }),
    )
    await page.goto(`/spaces/${spaceId}/tickets`)
    await expect(page.locator('text=Something went wrong')).not.toBeVisible()
  })

  test('wiki page renders when /wiki returns null', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Null Wiki', 'wiki')

    await page.route('**/api/v1/spaces/*/wiki', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: 'null' }),
    )
    await page.goto(`/spaces/${spaceId}/wiki`)
    await expect(page.locator('text=Something went wrong')).not.toBeVisible()
  })

  test('backlog renders when /projects/items returns null', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Null Backlog', 'project')

    await page.route('**/api/v1/spaces/*/projects/items', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: 'null' }),
    )
    await page.goto(`/spaces/${spaceId}/backlog`)
    await expect(page.locator('text=Something went wrong')).not.toBeVisible()
  })
})
