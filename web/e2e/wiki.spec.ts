import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors } from './helpers/setup'

test.describe('Wiki', () => {
  test('can create a wiki space and land on wiki view', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'E2E Wiki', 'wiki')
    await expect(page).toHaveURL(/\/spaces\/.*\/wiki/)
    await assertNoErrors(page)
  })

  test('wiki loads empty without error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Empty Wiki', 'wiki')
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('can create a wiki page and it appears in the tree', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Page Create Wiki', 'wiki')

    await page.click('button:has-text("New Page")')
    await expect(page.locator('#page-title')).toBeVisible()

    await page.fill('#page-title', 'E2E Test Page')
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()

    await expect(page.locator('text=E2E Test Page').first()).toBeVisible({ timeout: 5000 })
  })

  test('back to dashboard link works', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Nav Test Wiki', 'wiki')
    await page.click('text=Back to Dashboard')
    await expect(page).toHaveURL('/')
  })

  test.skip('wiki edit button opens editor — KNOWN GAP', async () => {
    // See docs/project-state.md Section 3 — Known Gaps
    // File: web/src/pages/wiki/WikiPage.tsx around line 150
    // Edit button exists with pencil icon but has no onClick handler
    // Re-enable after TipTap editor is implemented
  })

  test.skip('wiki page tree shows hierarchy — KNOWN GAP', async () => {
    // See docs/project-state.md Section 3 — Known Gaps
    // File: web/src/pages/wiki/WikiPage.tsx around line 91-135
    // Backend supports parent/child but frontend renders flat list only
  })

  test.skip('wiki page comments are visible — KNOWN GAP', async () => {
    // See docs/project-state.md Section 3 — Known Gaps
    // Comments backend exists but frontend wiring unconfirmed
  })
})
