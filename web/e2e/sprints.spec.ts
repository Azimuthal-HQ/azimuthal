import { test, expect, type Page } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors } from './helpers/setup'

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Creates a backlog item through the page's own dialog. */
/**
 * Walk an item from the project workflow's initial state to `done`.
 *
 * A single `selectOption('done')` used to work because the /status route wrote
 * whatever string it was given. It no longer does: the seeded project workflow
 * defines backlog -> in_progress -> in_review -> done and no shortcut, and now
 * that a configured workflow is ENFORCED the picker does not even offer `done`
 * from the backlog. That is the feature working; these tests want an item in a
 * done-category status, not a particular number of clicks, so they walk the
 * edges the workflow actually declares.
 *
 * Each hop asserts the value landed, so a broken hop fails here rather than
 * three lines later as a mystifying sprint-completion result.
 */
async function takeItemToDone(page: Page) {
  for (const status of ['in_progress', 'in_review', 'done']) {
    await page.getByLabel('Status').selectOption(status)
    await expect(page.getByLabel('Status')).toHaveValue(status)
  }
}

async function createItem(page: Page, title: string) {
  await page.click('button:has-text("Create Item")')
  await expect(page.locator('#item-title')).toBeVisible()
  await page.fill('#item-title', title)
  await page.locator('[role="dialog"] button:has-text("Create Item")').click()
  await expect(page.locator(`text=${title}`)).toBeVisible({ timeout: 5000 })
}

/** Creates a sprint through the Sprints page dialog. */
async function createSprint(page: Page, name: string) {
  await page.click('button:has-text("New Sprint")')
  await page.getByPlaceholder('Sprint 1').fill(name)
  await page.locator('[role="dialog"] button:has-text("Create Sprint")').click()
  await expect(page.getByRole('heading', { level: 2, name: new RegExp(name) }).or(
    page.locator(`text=${name}`).first(),
  )).toBeVisible({ timeout: 5000 })
}

/**
 * Re-sprints a backlog item via its row control. The row is found by title
 * rather than by item key, so the test does not depend on the random space
 * key that createSpace generates.
 */
async function setItemSprint(page: Page, title: string, sprintLabel: string) {
  const row = page.locator('tr', { hasText: title })
  await row.locator('select').selectOption({ label: sprintLabel })
}

/** The backlog group heading a given item currently sits under. */
async function groupOf(page: Page, title: string): Promise<string> {
  return page.evaluate((t) => {
    const cell = Array.from(document.querySelectorAll('td')).find(
      (td) => td.textContent?.trim() === t,
    )
    if (!cell) return '__item-not-found__'
    // Walk back from the item's table to the group's own <h2>.
    const section = cell.closest('div.space-y-2')
    return section?.querySelector('h2')?.textContent?.replace(/\s*\(\d+ items\)\s*$/, '').trim()
      ?? '__no-heading__'
  }, title)
}

test.describe('Sprints', () => {
  // Regression: the backend persists new sprints with status "planned"
  // (SprintStatusPlanned, enforced by the CHECK constraint in 004_items.sql),
  // but the frontend gated the Start button on a 'planning' literal that the
  // API can never return — so a freshly created sprint had no Start button
  // and could never be started from the UI.
  test('newly created sprint shows Start button — status literal matches backend "planned" (regression)', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Sprint Start Regression', 'vector')

    await page.goto(`/vector/${spaceId}/sprints`)
    await expect(page.locator('h1:has-text("Sprints")')).toBeVisible({ timeout: 10000 })

    // Create a sprint through the page's own dialog.
    await page.click('button:has-text("New Sprint")')
    await page.getByPlaceholder('Sprint 1').fill('Regression Sprint')
    await page.locator('[role="dialog"] button:has-text("Create Sprint")').click()

    // The row appears, carrying the status literal the backend persisted.
    await expect(page.locator('text=Regression Sprint')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=planned')).toBeVisible()

    // A planned sprint must offer Start. This is the defect: with the
    // frontend expecting 'planning', the button never rendered. Exact name:
    // the sidebar space picker's accessible name contains this space's own
    // name ("Sprint Start Regression …"), which a substring match would hit.
    const startButton = page.getByRole('button', { name: 'Start', exact: true })
    await expect(startButton).toBeVisible()

    // And the transition works end-to-end: planned → active.
    await startButton.click()
    await expect(page.getByRole('button', { name: 'Complete', exact: true })).toBeVisible({ timeout: 5000 })
    await assertNoErrors(page)
  })

  // -------------------------------------------------------------------------
  // Full lifecycle journeys (E2E backfill owed from #67, which shipped W1's
  // completion disposition with no Playwright coverage at all).
  //
  // Both journeys assert the disposition contract from both sides: done items
  // STAY on the completed sprint, and incomplete items land exactly where the
  // dialog said. A test that only checked the incomplete item would pass even
  // if completion swept the done ones off too.
  // -------------------------------------------------------------------------

  test('create → add items → start → complete, returning incomplete work to the backlog', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Sprint Lifecycle Backlog', 'vector')

    await createItem(page, 'Alpha Done')
    await createItem(page, 'Alpha Todo')

    await page.goto(`/vector/${spaceId}/sprints`)
    await createSprint(page, 'Alpha Sprint')

    // W2: assign both items to the sprint from the backlog's row control.
    await page.goto(`/vector/${spaceId}/backlog`)
    await setItemSprint(page, 'Alpha Done', 'Alpha Sprint')
    await setItemSprint(page, 'Alpha Todo', 'Alpha Sprint')

    await expect.poll(() => groupOf(page, 'Alpha Done')).toBe('Alpha Sprint')
    await expect.poll(() => groupOf(page, 'Alpha Todo')).toBe('Alpha Sprint')

    // The group heading is the sprint's NAME. Before W2 this rendered the raw
    // sprint UUID, so this assertion is also the regression guard for that.
    await expect(page.getByRole('heading', { level: 2, name: /Alpha Sprint/ })).toBeVisible()

    // Take one item to a done status.
    await page.click('text=Alpha Done')
    await expect(page).toHaveURL(/\/backlog\/[0-9a-f-]{36}/, { timeout: 10000 })
    await takeItemToDone(page)

    // Start and complete, choosing the backlog disposition.
    await page.goto(`/vector/${spaceId}/sprints`)
    await page.getByRole('button', { name: 'Start', exact: true }).click()
    await page.getByRole('button', { name: 'Complete', exact: true }).click()

    const dialog = page.locator('[role="dialog"]')
    await expect(dialog).toBeVisible()
    await dialog.getByTestId('complete-disposition')
      .locator('[data-value="backlog"]').click()
    await dialog.getByRole('button', { name: 'Complete Sprint' }).click()
    await expect(dialog).not.toBeVisible({ timeout: 10000 })

    // The contract, both halves.
    await page.goto(`/vector/${spaceId}/backlog`)
    await expect.poll(() => groupOf(page, 'Alpha Todo'), { timeout: 10000 }).toBe('Backlog')
    await expect.poll(() => groupOf(page, 'Alpha Done')).toBe('Alpha Sprint')
    await assertNoErrors(page)
  })

  test('completing a sprint can carry incomplete work over to a chosen next sprint', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Sprint Lifecycle Carry', 'vector')

    await createItem(page, 'Shipped Item')
    await createItem(page, 'Carried Item')

    await page.goto(`/vector/${spaceId}/sprints`)
    await createSprint(page, 'Alpha Sprint')
    await createSprint(page, 'Beta Sprint')

    await page.goto(`/vector/${spaceId}/backlog`)
    await setItemSprint(page, 'Shipped Item', 'Alpha Sprint')
    await setItemSprint(page, 'Carried Item', 'Alpha Sprint')
    await expect.poll(() => groupOf(page, 'Carried Item')).toBe('Alpha Sprint')

    await page.click('text=Shipped Item')
    await expect(page).toHaveURL(/\/backlog\/[0-9a-f-]{36}/, { timeout: 10000 })
    await takeItemToDone(page)

    await page.goto(`/vector/${spaceId}/sprints`)
    // Only the planned sprints offer Start; Alpha is the first row.
    const alphaRow = page.locator('div', { hasText: /^Alpha Sprint/ }).first()
    await alphaRow.getByRole('button', { name: 'Start', exact: true }).click()
    await page.getByRole('button', { name: 'Complete', exact: true }).click()

    const dialog = page.locator('[role="dialog"]')
    await expect(dialog).toBeVisible()
    await dialog.getByTestId('complete-disposition')
      .locator('[data-value="next"]').click()
    await dialog.getByLabel('Carry-over sprint').selectOption({ label: 'Beta Sprint' })
    await dialog.getByRole('button', { name: 'Complete Sprint' }).click()
    await expect(dialog).not.toBeVisible({ timeout: 10000 })

    // Incomplete work carried to Beta; done work stayed on Alpha.
    await page.goto(`/vector/${spaceId}/backlog`)
    await expect.poll(() => groupOf(page, 'Carried Item'), { timeout: 10000 }).toBe('Beta Sprint')
    await expect.poll(() => groupOf(page, 'Shipped Item')).toBe('Alpha Sprint')
    await assertNoErrors(page)
  })

  test('item detail can move an item between sprints and the backlog', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Item Detail Sprint', 'vector')

    await createItem(page, 'Detail Item')
    await page.goto(`/vector/${spaceId}/sprints`)
    await createSprint(page, 'Detail Sprint')

    await page.goto(`/vector/${spaceId}/backlog`)
    await page.click('text=Detail Item')
    await expect(page).toHaveURL(/\/backlog\/[0-9a-f-]{36}/, { timeout: 10000 })

    const sprintSelect = page.getByLabel('Sprint')
    await expect(sprintSelect).toHaveValue('__backlog__')

    // Wait for the assignment POST to land before navigating away.
    // selectOption resolves when the DOM event dispatches, not when the write
    // does, and the goto right after can cancel the request in flight — the
    // same race the due-date spec's setDueDate helper documents, surfacing
    // here as the move intermittently never happening.
    const moved = page.waitForResponse(
      (r) => r.request().method() === 'POST' && /\/sprint$/.test(r.url()),
    )
    await sprintSelect.selectOption({ label: 'Detail Sprint' })
    expect((await moved).status(), 'the sprint assignment must land before navigating').toBe(200)

    await page.goto(`/vector/${spaceId}/backlog`)
    await expect.poll(() => groupOf(page, 'Detail Item'), { timeout: 10000 }).toBe('Detail Sprint')

    // And back off the sprint again.
    await page.click('text=Detail Item')
    const returned = page.waitForResponse(
      (r) => r.request().method() === 'POST' && /\/sprint$/.test(r.url()),
    )
    await page.getByLabel('Sprint').selectOption('__backlog__')
    expect((await returned).status(), 'the backlog return must land before navigating').toBe(200)
    await page.goto(`/vector/${spaceId}/backlog`)
    await expect.poll(() => groupOf(page, 'Detail Item'), { timeout: 10000 }).toBe('Backlog')
    await assertNoErrors(page)
  })

  test('backlog multi-select moves several items to a sprint at once', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Bulk Sprint Move', 'vector')

    await createItem(page, 'Bulk One')
    await createItem(page, 'Bulk Two')

    await page.goto(`/vector/${spaceId}/sprints`)
    await createSprint(page, 'Bulk Sprint')

    await page.goto(`/vector/${spaceId}/backlog`)
    await page.locator('tr', { hasText: 'Bulk One' }).locator('input[type="checkbox"]').check()
    await page.locator('tr', { hasText: 'Bulk Two' }).locator('input[type="checkbox"]').check()

    const bar = page.getByTestId('backlog-bulk-bar')
    await expect(bar).toContainText('2 selected')
    await bar.getByLabel('Move selected to sprint').selectOption({ label: 'Bulk Sprint' })

    await expect.poll(() => groupOf(page, 'Bulk One'), { timeout: 10000 }).toBe('Bulk Sprint')
    await expect.poll(() => groupOf(page, 'Bulk Two')).toBe('Bulk Sprint')
    // The bar clears once the move lands.
    await expect(bar).not.toBeVisible()
    await assertNoErrors(page)
  })
})
