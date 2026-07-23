import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors, getAuthToken, getCurrentUser } from './helpers/setup'

// Codex has ONE navigation panel (ADR-0005): the shell sidebar owns the page
// tree, the scoped search, and the create affordance. A page title therefore
// appears in exactly two places — the sidebar tree and the content header —
// so every title locator below scopes to one of them (P1.5 exactness): the
// tree via getByTestId('codex-page-tree'), the header via
// getByTestId('wiki-page-title').

test.describe('Wiki', () => {
  test('can create a wiki space and land on wiki view', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'E2E Wiki', 'codex')
    await expect(page).toHaveURL(/\/codex\//)
    await assertNoErrors(page)
  })

  test('wiki loads empty without error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Empty Wiki', 'codex')
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('can create a wiki page and it appears in the tree', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Page Create Wiki', 'codex')

    await page.getByRole('button', { name: 'New page' }).click()
    await expect(page.locator('#page-title')).toBeVisible()

    await page.fill('#page-title', 'E2E Test Page')
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()

    // The new page lands in the sidebar tree and opens as the current page —
    // with the tree row marked active (aria-current comes from NavLink).
    const newRow = page.getByTestId('codex-page-tree').getByRole('link', { name: 'E2E Test Page' })
    await expect(newRow).toBeVisible({ timeout: 5000 })
    await expect(newRow).toHaveAttribute('aria-current', 'page')
    await expect(page.getByTestId('wiki-page-title')).toContainText('E2E Test Page', { timeout: 5000 })
  })

  test('Home product tab returns to the overview from a wiki', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Nav Test Wiki', 'codex')
    await page.getByTestId('product-tab-home').click()
    await expect(page).toHaveURL('/')
  })

  test('wiki edit button opens editor and edit persists', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Editor Wiki', 'codex')

    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', 'Editable Page')
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.getByTestId('wiki-page-title')).toContainText('Editable Page', { timeout: 5000 })

    // Open the editor (TipTap-based rich text editor). Use an exact-name
    // locator: a substring match on "Edit" would also match a page titled
    // "Editable…" elsewhere on the page.
    await page.getByRole('button', { name: 'Edit', exact: true }).click()
    const editor = page.locator('.ProseMirror')
    await expect(editor).toBeVisible({ timeout: 5000 })

    // Type content and save
    await editor.click()
    await editor.pressSequentially('Content written in the editor')
    await page.getByRole('button', { name: 'Save', exact: true }).click()

    // Save must actually exit edit mode (the read-mode Edit button returns).
    // The TipTap editor is contentEditable, so the typed text is live DOM
    // that text= matches even if the save request never fired — and on a
    // failed save the page stays in edit mode with the text still mounted.
    // Asserting read mode first makes the content check mean "rendered from
    // the saved page", then the reload proves persistence. Scoped to the
    // read-mode article so nothing else on the page can satisfy it.
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 5000 })
    await expect(page.locator('article').getByText('Content written in the editor')).toBeVisible({ timeout: 5000 })
    await page.reload()
    await expect(page.locator('article').getByText('Content written in the editor')).toBeVisible({ timeout: 10000 })
  })

  test('wiki page tree shows hierarchy', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Tree Wiki', 'codex')

    // Create a top-level page…
    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', 'Parent Node')
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(
      page.getByTestId('codex-page-tree').getByText('Parent Node', { exact: true }),
    ).toBeVisible({ timeout: 5000 })

    // …and a child nested under it via the parent selector.
    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', 'Child Node')
    await page.selectOption('#page-parent', { label: 'Parent Node' })
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()

    // The sidebar tree must render real hierarchy: the child sits at depth 1
    // under its parent — not in a flat list. The depth attributes live on
    // the sidebar tree rows since the navigation collapse.
    const childEntry = page.locator('[data-tree-depth="1"]').filter({ hasText: 'Child Node' })
    await expect(childEntry).toBeVisible({ timeout: 5000 })
    const parentEntry = page.locator('[data-tree-depth="0"]').filter({ hasText: 'Parent Node' })
    await expect(parentEntry).toBeVisible()
  })

  test('deep tree scrolls independently — fixed zone never leaves the viewport', async ({ page }) => {
    // ADR-0005's load-bearing clause: "a large wiki must never push those
    // out of reach." The tree scrolls in its own container; the fixed zone
    // (search, Recent, Starred, Drafts) stays put. A small fixture proves
    // nothing, so this seeds 64 pages.
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)
    const spaceId = await createSpace(page, 'Deep Tree Wiki', 'codex')
    const token = await getAuthToken(page)

    for (let i = 1; i <= 64; i++) {
      const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/wiki`, {
        headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
        data: { title: `Deep Page ${String(i).padStart(2, '0')}`, content: '' },
      })
      expect(res.status()).toBe(201)
    }

    await page.goto(`/codex/${spaceId}`)
    const tree = page.getByTestId('codex-page-tree')
    await expect(tree.locator('[data-tree-depth]')).toHaveCount(64, { timeout: 10000 })

    // The tree overflows and scrolls fully inside its own container.
    const metrics = await tree.evaluate((el) => {
      el.scrollTop = el.scrollHeight
      return { scrollTop: el.scrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight }
    })
    expect(metrics.scrollTop).toBeGreaterThan(0)
    expect(metrics.scrollTop + metrics.clientHeight).toBeGreaterThanOrEqual(metrics.scrollHeight - 1)

    // A named deep page is reachable by scrolling the tree alone. (Sibling
    // order from the API is not title-order, so the row is scrolled to
    // explicitly rather than assumed to be last.)
    const target = tree.locator('[data-tree-depth]').filter({ hasText: 'Deep Page 64' })
    await target.scrollIntoViewIfNeeded()
    await expect(target).toBeInViewport()

    // The fixed zone never left the viewport, and nothing but the tree
    // scrolled to make that reachability happen.
    const sidebar = page.getByTestId('space-sidebar')
    expect(await sidebar.evaluate((el) => el.scrollTop)).toBe(0)
    expect(await page.evaluate(() => window.scrollY)).toBe(0)
    await expect(page.getByTestId('codex-page-search')).toBeInViewport()
    await expect(sidebar.getByRole('link', { name: 'Recent', exact: true })).toBeInViewport()
    await expect(sidebar.getByRole('link', { name: 'Starred', exact: true })).toBeInViewport()
    await expect(sidebar.getByRole('link', { name: 'Drafts', exact: true })).toBeInViewport()
    await assertNoErrors(page)
  })

  test('wiki page comments are visible and can be posted', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Comments Wiki', 'codex')

    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', 'Commented Page')
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.getByTestId('wiki-page-title')).toContainText('Commented Page', { timeout: 5000 })

    // Post a comment and verify it renders and persists after reload.
    const commentBox = page.getByPlaceholder('Add a comment...')
    await expect(commentBox).toBeVisible({ timeout: 5000 })
    await commentBox.fill('A wiki page comment')
    // Exact-name locator: the sidebar tree row "Commented Page" would
    // otherwise match a substring "Comment" search.
    await page.getByRole('button', { name: 'Comment', exact: true }).click()
    await expect(page.getByText('A wiki page comment', { exact: true })).toBeVisible({ timeout: 5000 })

    await page.reload()
    await expect(page.getByText('A wiki page comment', { exact: true })).toBeVisible({ timeout: 10000 })
  })
})
