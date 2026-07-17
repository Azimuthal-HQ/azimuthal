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

    await page.getByRole('button', { name: 'New page' }).click()
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

  test('wiki edit button opens editor and edit persists', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Editor Wiki', 'wiki')

    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', 'Editable Page')
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.locator('text=Editable Page').first()).toBeVisible({ timeout: 5000 })

    // Open the editor (TipTap-based rich text editor). Use an exact-name
    // locator: the sidebar renders each page title as a button, so a
    // substring match on "Edit" would also match a page titled "Editable…".
    await page.getByRole('button', { name: 'Edit', exact: true }).click()
    const editor = page.locator('.ProseMirror').first()
    await expect(editor).toBeVisible({ timeout: 5000 })

    // Type content and save
    await editor.click()
    await editor.pressSequentially('Content written in the editor')
    await page.getByRole('button', { name: 'Save', exact: true }).click()

    // Content renders after save, and persists across reload
    await expect(page.locator('text=Content written in the editor').first()).toBeVisible({ timeout: 5000 })
    await page.reload()
    await expect(page.locator('text=Content written in the editor').first()).toBeVisible({ timeout: 10000 })
  })

  test('wiki page tree shows hierarchy', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Tree Wiki', 'wiki')

    // Create a top-level page…
    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', 'Parent Node')
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.locator('text=Parent Node').first()).toBeVisible({ timeout: 5000 })

    // …and a child nested under it via the parent selector.
    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', 'Child Node')
    await page.selectOption('#page-parent', { label: 'Parent Node' })
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.locator('text=Child Node').first()).toBeVisible({ timeout: 5000 })

    // The sidebar must render real hierarchy: the child sits at depth 1
    // under its parent — not in a flat list.
    const childEntry = page.locator('[data-tree-depth="1"]').filter({ hasText: 'Child Node' })
    await expect(childEntry).toBeVisible({ timeout: 5000 })
    const parentEntry = page.locator('[data-tree-depth="0"]').filter({ hasText: 'Parent Node' }).first()
    await expect(parentEntry).toBeVisible()
  })

  test('wiki page comments are visible and can be posted', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Comments Wiki', 'wiki')

    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', 'Commented Page')
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.locator('text=Commented Page').first()).toBeVisible({ timeout: 5000 })

    // Post a comment and verify it renders and persists after reload.
    const commentBox = page.locator('textarea[placeholder*="comment" i], input[placeholder*="comment" i]').first()
    await expect(commentBox).toBeVisible({ timeout: 5000 })
    await commentBox.fill('A wiki page comment')
    // Exact-name locator: the sidebar page button "Commented Page" would
    // otherwise match a substring "Comment" search.
    await page.getByRole('button', { name: 'Comment', exact: true }).click()
    await expect(page.locator('text=A wiki page comment').first()).toBeVisible({ timeout: 5000 })

    await page.reload()
    await expect(page.locator('text=A wiki page comment').first()).toBeVisible({ timeout: 10000 })
  })
})
