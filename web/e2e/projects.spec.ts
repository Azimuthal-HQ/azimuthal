import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors, getAuthToken } from './helpers/setup'

test.describe('Projects', () => {
  test('can create a project space and land on backlog', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'E2E Project', 'project')
    await expect(page).toHaveURL(/\/spaces\/.*\/backlog/, { timeout: 10000 })
    await expect(page.locator('text=Backlog').first()).toBeVisible()
    await assertNoErrors(page)
  })

  test('backlog loads empty without error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Empty Project', 'project')
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('can create a backlog item and it appears', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Item Create Project', 'project')

    await page.click('button:has-text("Create Item")')
    await expect(page.locator('#item-title')).toBeVisible()

    await page.fill('#item-title', 'E2E Test Item')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()

    await expect(page.locator('text=E2E Test Item')).toBeVisible({ timeout: 5000 })
  })

  test.fixme('created item shows correct priority — not Unknown', async ({ page }) => {
    // KNOWN BUG: priority badge shows "Unknown" instead of "Medium" for new items
    await createUserAndLogin(page)
    await createSpace(page, 'Priority Project', 'project')

    await page.click('button:has-text("Create Item")')
    await page.fill('#item-title', 'Priority Check Item')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()

    await expect(page.locator('text=Priority Check Item')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('sprint board loads without error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Sprint Test', 'project')
    await page.click('text=Sprint Board')
    await assertNoErrors(page)
  })

  test('back to dashboard link works', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Nav Test Project', 'project')
    await page.click('text=Back to Dashboard')
    await expect(page).toHaveURL('/')
  })

  test.skip('clicking a backlog item opens detail view — KNOWN GAP', async () => {
    // See docs/project-state.md Section 3 — Known Gaps
    // File: web/src/pages/projects/ItemDetailPage.tsx
    // Detail view exists but is read-only with no edit capability
  })
})
