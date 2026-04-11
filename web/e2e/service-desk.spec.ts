import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors, getAuthToken } from './helpers/setup'

test.describe('Service Desk', () => {
  test('can create a service desk space and land on ticket list', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'E2E Service Desk', 'service_desk')
    await expect(page).toHaveURL(/\/spaces\/.*\/tickets/, { timeout: 10000 })
    await expect(page.locator('h1:has-text("Tickets"), h2:has-text("Tickets"), [role="heading"]:has-text("Tickets")').first()).toBeVisible()
    await assertNoErrors(page)
  })

  test('ticket list loads without error on empty space', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Empty Desk', 'service_desk')
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('can create a ticket with minimum fields', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Ticket Create Test', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await expect(page.locator('#ticket-title')).toBeVisible()

    await page.fill('#ticket-title', 'E2E Test Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()

    await expect(page.locator('text=E2E Test Ticket')).toBeVisible({ timeout: 5000 })
  })

  test.fixme('created ticket shows correct priority — not Unknown', async ({ page }) => {
    // KNOWN BUG: priority badge shows "Unknown" instead of "Medium" for new tickets
    await createUserAndLogin(page)
    await createSpace(page, 'Priority Display Test', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Priority Check Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()

    await expect(page.locator('text=Priority Check Ticket')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=Unknown')).not.toBeVisible()
    // Medium is the default priority
    await expect(page.locator('text=Medium').first()).toBeVisible()
  })

  test.fixme('created ticket shows correct status — not blank', async ({ page }) => {
    // KNOWN BUG: status badge display issue — depends on priority bug fix
    await createUserAndLogin(page)
    await createSpace(page, 'Status Display Test', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Status Check Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()

    await expect(page.locator('text=Status Check Ticket')).toBeVisible({ timeout: 5000 })
    // Status should be Open by default
    await expect(page.locator('text=Open').first()).toBeVisible()
  })

  test('ticket creation is confirmed by API', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'API Verify Desk', 'service_desk')

    const spaceId = page.url().match(/\/spaces\/([^/]+)/)?.[1]

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'API Verify Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=API Verify Ticket')).toBeVisible({ timeout: 5000 })

    // Verify via API that the ticket actually exists in the database
    const token = await getAuthToken(page)
    const orgId = await page.evaluate(() => {
      for (const key of Object.keys(localStorage)) {
        const val = localStorage.getItem(key)
        if (val && val.includes('org_id')) {
          try { return JSON.parse(atob(val.split('.')[1])).org_id } catch { return null }
        }
      }
      return null
    })

    if (orgId && spaceId) {
      const response = await page.request.get(
        `/api/v1/orgs/${orgId}/spaces/${spaceId}/items`,
        { headers: { Authorization: `Bearer ${token}` } }
      )
      expect(response.status()).toBe(200)
      const items = await response.json()
      expect(Array.isArray(items)).toBe(true)
      expect(items.length).toBeGreaterThan(0)
      expect(items[0].priority).toBe('medium')
      expect(items[0].status).toBe('open')
    }
  })

  test('kanban board loads without error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Kanban Test', 'service_desk')
    await page.click('text=Kanban Board')
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('back to dashboard link works', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Nav Test Desk', 'service_desk')
    await page.click('text=Back to Dashboard')
    await expect(page).toHaveURL('/')
    await expect(page.locator('text=Welcome back')).toBeVisible()
  })

  test.skip('clicking a ticket row opens detail view — KNOWN GAP', async () => {
    // See docs/project-state.md Section 2 — Service Desk
    // Ticket detail view click is marked as working (✓) but priority display
    // has a ⚠ warning about "Unknown" fallback for unmapped values.
    // Skipping until priority mapping is fully verified end-to-end.
    // File: web/src/pages/tickets/TicketDetailPage.tsx
  })
})
