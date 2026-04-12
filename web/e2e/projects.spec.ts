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

  test('project item status can be changed', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Status Change Project', 'project')
    await page.click('button:has-text("Create Item")')
    await page.fill('#item-title', 'Status Test Item')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()
    await expect(page.locator('text=Status Test Item')).toBeVisible({ timeout: 5000 })
    await page.click('text=Status Test Item')
    await expect(page).not.toHaveURL(/\/login/)

    // Find status dropdown and change it
    const statusSelect = page.locator('select').filter({ hasText: 'Open' }).first()
    await expect(statusSelect).toBeVisible({ timeout: 5000 })
    await statusSelect.selectOption('in_progress')

    // Reload and verify status persisted — use Badge element to avoid matching dropdown option
    await page.reload()
    await expect(page.locator('[class*="inline-flex"]:has-text("In Progress")').first()).toBeVisible({ timeout: 5000 })
  })

  test('no 404 errors in network tab when viewing project item detail', async ({ page }) => {
    const failedRequests: string[] = []
    page.on('response', response => {
      if (response.status() === 404) {
        failedRequests.push(`404: ${response.url()}`)
      }
    })

    await createUserAndLogin(page)
    await createSpace(page, 'No 404 Project', 'project')
    await page.click('button:has-text("Create Item")')
    await page.fill('#item-title', 'No 404 Item')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()
    await expect(page.locator('text=No 404 Item')).toBeVisible({ timeout: 5000 })
    await page.click('text=No 404 Item')
    await expect(page).not.toHaveURL(/\/login/)
    await page.waitForTimeout(2000)

    const api404s = failedRequests.filter(r => r.includes('/api/'))
    expect(api404s, `Unexpected API 404s: ${api404s.join(', ')}`).toHaveLength(0)
  })
})
