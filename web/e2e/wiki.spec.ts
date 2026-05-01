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

  test.fixme('wiki edit button opens editor', async () => {
    // FEATURE GAP (not a bug): the wiki edit pencil button has no onClick handler because the TipTap-based editor component has not been built yet.
    // Audit ref: testing-audit.md §3.3
    // Re-enable when: the editor component is wired and pointed at PUT /api/v1/spaces/{spaceID}/wiki/{pageID}.
  })

  test.fixme('wiki page tree shows hierarchy', async () => {
    // FEATURE GAP (not a bug): the wiki sidebar renders a flat list even though pages.parent_id supports nesting.
    // Audit ref: testing-audit.md §3.3
    // Re-enable when: the frontend renders parent_id as a tree instead of a flat list.
  })

  test.fixme('wiki page comments are visible', async () => {
    // FEATURE GAP (not a bug): the wiki page does not call the comments endpoint or render a comment thread.
    // Audit ref: testing-audit.md §3.3
    // Re-enable when: the frontend posts to and renders from /api/v1/orgs/{orgID}/spaces/{spaceID}/items/{itemID}/comments.
  })
})
