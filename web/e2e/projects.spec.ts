import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors, getAuthToken } from './helpers/setup'

test.describe('Projects', () => {
  test('can create a project space and land on backlog', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'E2E Project', 'vector')
    await expect(page).toHaveURL(/\/vector\/.*\/backlog/, { timeout: 10000 })
    await expect(page.locator('text=Backlog').first()).toBeVisible()
    await assertNoErrors(page)
  })

  test('backlog loads empty without error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Empty Project', 'vector')
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('can create a backlog item and it appears', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Item Create Project', 'vector')

    await page.click('button:has-text("Create Item")')
    await expect(page.locator('#item-title')).toBeVisible()

    await page.fill('#item-title', 'E2E Test Item')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()

    await expect(page.locator('text=E2E Test Item')).toBeVisible({ timeout: 5000 })
  })

  test('created item shows correct priority — not Unknown', async ({ page }) => {
    // Audit ref: testing-audit.md §3.3.
    // BacklogPage's STATUS_LABEL/PRIORITY_LABEL now cover the keys the
    // backend actually returns ("open", "medium"), so unmapped fallbacks
    // never surface as "Unknown".
    await createUserAndLogin(page)
    await createSpace(page, 'Priority Project', 'vector')

    await page.click('button:has-text("Create Item")')
    await page.fill('#item-title', 'Priority Check Item')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()

    await expect(page.locator('text=Priority Check Item')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('sprint with start and end dates can be created — regression: 400 invalid request body', async ({ page }) => {
    // P0 defect: the New Sprint dialog posted bare YYYY-MM-DD strings from its
    // date inputs; the API's RFC3339 timestamp contract rejected them with
    // 400 "invalid request body", so every dated sprint creation failed.
    await createUserAndLogin(page)
    await createSpace(page, 'Sprint Dates Project', 'vector')

    await page.getByTestId('space-sidebar').getByRole('link', { name: 'Sprints', exact: true }).click()
    await expect(page).toHaveURL(/\/vector\/.*\/sprints/, { timeout: 10000 })

    await page.click('button:has-text("New Sprint")')
    await page.fill('input[placeholder="Sprint 1"]', 'Dated Sprint')
    await page.locator('input[type="date"]').first().fill('2026-07-20')
    await page.locator('input[type="date"]').nth(1).fill('2026-08-03')
    await page.locator('[role="dialog"] button:has-text("Create Sprint")').click()

    // The sprint must appear with its dates — and no 400 error text anywhere.
    await expect(page.locator('text=Dated Sprint')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=2026-07-20')).toBeVisible()
    await assertNoErrors(page)
  })

  test('priority selector is one connected segmented control — regression: cramped chips', async ({ page }) => {
    // P0 defect: the create-item form rendered Critical/High/Medium/Low as
    // four loose content-width chips with almost no padding. Rebuilt as a
    // single segmented control: equal option widths above a sane minimum,
    // options joined edge-to-edge with no gaps.
    await createUserAndLogin(page)
    await createSpace(page, 'Priority Segments Project', 'vector')

    await page.click('button:has-text("Create Item")')
    const group = page.getByRole('radiogroup', { name: 'Priority' })
    await expect(group).toBeVisible({ timeout: 5000 })

    const options = group.getByRole('radio')
    await expect(options).toHaveCount(4)

    const boxes = []
    for (let i = 0; i < 4; i++) {
      const box = await options.nth(i).boundingBox()
      if (!box) throw new Error(`priority option ${i} has no bounding box`)
      boxes.push(box)
    }

    // Equal widths (sub-pixel tolerance) above a usable minimum.
    const widths = boxes.map((b) => b.width)
    expect(Math.max(...widths) - Math.min(...widths)).toBeLessThanOrEqual(1.5)
    expect(Math.min(...widths)).toBeGreaterThanOrEqual(64)

    // Connected: adjacent options touch (divider only, no gaps between them).
    for (let i = 1; i < 4; i++) {
      const gap = boxes[i].x - (boxes[i - 1].x + boxes[i - 1].width)
      expect(gap).toBeLessThanOrEqual(2)
    }

    // Still functional: selecting an option marks it checked and the default
    // selection (Medium) starts out checked.
    await expect(options.nth(2)).toHaveAttribute('aria-checked', 'true')
    await options.nth(0).click()
    await expect(options.nth(0)).toHaveAttribute('aria-checked', 'true')
    await expect(options.nth(2)).toHaveAttribute('aria-checked', 'false')
  })

  test('Labels view renders content — regression: blank screen', async ({ page }) => {
    // P0 defect: the sidebar linked to /spaces/:id/labels but no route existed,
    // so React Router rendered nothing — an entirely blank document body.
    await createUserAndLogin(page)
    await createSpace(page, 'Labels Project', 'vector')

    await page.getByTestId('space-sidebar').getByRole('link', { name: 'Labels', exact: true }).click()
    await expect(page).toHaveURL(/\/vector\/.*\/labels/, { timeout: 10000 })

    // The route must render non-empty content — a blank body is never acceptable.
    await expect(page.locator('h1:has-text("Labels")')).toBeVisible({ timeout: 5000 })
    expect((await page.locator('body').innerText()).trim()).not.toBe('')
    await assertNoErrors(page)
  })

  test('sprint board loads without error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Sprint Test', 'vector')
    await page.getByTestId('space-sidebar').getByRole('link', { name: 'Board', exact: true }).click()
    await assertNoErrors(page)
  })

  test('Home product tab returns to the overview from a project', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Nav Test Project', 'vector')
    await page.getByTestId('product-tab-home').click()
    await expect(page).toHaveURL('/')
  })

  test('clicking a backlog item opens detail view with edit capability', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Edit Capability Project', 'vector')
    await page.click('button:has-text("Create Item")')
    await page.fill('#item-title', 'Editable Item')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()
    await expect(page.locator('text=Editable Item')).toBeVisible({ timeout: 5000 })
    await page.click('text=Editable Item')
    await expect(page).not.toHaveURL(/\/login/)

    // Enter edit mode, change title and description, save. Exact-name role
    // locators: the space picker's accessible name ("Edit Capability
    // Project …") would collide with a substring "Edit" match.
    await page.getByRole('button', { name: 'Edit', exact: true }).click()
    await page.fill('#edit-item-title', 'Editable Item Renamed')
    await page.fill('#edit-item-description', 'Updated via edit form')
    await page.getByRole('button', { name: 'Save', exact: true }).click()
    await expect(page.locator('h1:has-text("Editable Item Renamed")')).toBeVisible({ timeout: 5000 })

    // Reload — the edit persisted to the database.
    await page.reload()
    await expect(page.locator('h1:has-text("Editable Item Renamed")')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=Updated via edit form')).toBeVisible()
  })

  test('project item status can be changed', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Status Change Project', 'vector')
    await page.click('button:has-text("Create Item")')
    await page.fill('#item-title', 'Status Test Item')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()
    await expect(page.locator('text=Status Test Item')).toBeVisible({ timeout: 5000 })
    await page.click('text=Status Test Item')
    await expect(page).not.toHaveURL(/\/login/)

    // Find status dropdown and change it
    const statusSelect = page.getByLabel('Status')
    await expect(statusSelect).toBeVisible({ timeout: 5000 })
    await statusSelect.selectOption('in_progress')

    // Reload and verify status persisted — use Badge element to avoid matching dropdown option
    await page.reload()
    await expect(page.locator('[class*="inline-flex"]:has-text("In Progress")').first()).toBeVisible({ timeout: 5000 })
  })

  test('project item status change persists after page reload', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Status Persist Project', 'vector')
    await page.click('button:has-text("Create Item")')
    await page.fill('#item-title', 'Status Persist Item')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()
    await expect(page.locator('text=Status Persist Item')).toBeVisible({ timeout: 5000 })
    await page.click('text=Status Persist Item')
    await expect(page).not.toHaveURL(/\/login/)

    // Change status to In Progress
    const statusSelect = page.getByLabel('Status')
    await expect(statusSelect).toBeVisible({ timeout: 5000 })
    await statusSelect.selectOption('in_progress')

    // Wait for save
    await page.waitForTimeout(1000)

    // Reload and verify status persisted — this is the critical check
    await page.reload()
    await expect(page).not.toHaveURL(/\/login/)

    // Status select must show in_progress after reload — not revert to open
    await expect(page.getByLabel('Status')).toHaveValue('in_progress', { timeout: 5000 })
  })

  test('no 404 errors in network tab when viewing project item detail', async ({ page }) => {
    const failedRequests: string[] = []
    page.on('response', response => {
      if (response.status() === 404) {
        failedRequests.push(`404: ${response.url()}`)
      }
    })

    await createUserAndLogin(page)
    await createSpace(page, 'No 404 Project', 'vector')
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
