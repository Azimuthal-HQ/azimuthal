import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors } from './helpers/setup'

/**
 * A6: the due-date lockout, end to end.
 *
 * A workflow transition guard can require `due_at`, and no surface in the
 * product could set one. On the Beacon side that was not a missing control but
 * a missing write path — the ticket PATCH rejected the field outright — so the
 * only proof that the lockout is actually closed is a real browser putting a
 * date on a real ticket and the real server still having it after a reload.
 *
 * The reload is the assertion. Both controls refetch after their PATCH, so a
 * date that only ever reached React state would look identical on screen to one
 * that reached postgres. Reloading is what tells them apart.
 */

const DUE = '2026-09-01'

/**
 * Set (or clear) a due date and wait for the write to actually land.
 *
 * The control PATCHes on change with nothing awaited in the DOM, so
 * `fill(...)` followed by `reload()` is a race: the reload can cancel the
 * request in flight and the assertion afterwards then describes the browser's
 * timing rather than the server's state. The first draft of this spec had
 * exactly that bug — the clear case failed, and the two set cases were passing
 * only because the PATCH happened to win.
 *
 * Waiting on the response is what makes the reload mean something. It is also
 * why this is not a `waitForTimeout`: a sleep long enough to be reliable is
 * long enough to hide a regression in how long the write takes.
 */
async function setDueDate(
  page: import('@playwright/test').Page,
  testId: string,
  value: string,
  pathFragment: RegExp,
) {
  const patched = page.waitForResponse(
    (r) => r.request().method() === 'PATCH' && pathFragment.test(r.url()),
  )
  await page.getByTestId(testId).fill(value)
  const res = await patched
  expect(res.status(), `PATCH ${res.url()} must succeed`).toBe(200)
}

test.describe('Due date', () => {
  test('a due date set on a ticket persists across a reload', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Due Date Desk', 'beacon')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Ticket With A Deadline')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Ticket With A Deadline')).toBeVisible({ timeout: 5000 })

    await page.click('text=Ticket With A Deadline')
    await expect(page).toHaveURL(/\/tickets\//, { timeout: 5000 })

    const due = page.getByTestId('ticket-due-date')
    await expect(due).toBeVisible()
    // Premise: a ticket is born without one. Without this the test would pass
    // against a control that rendered a date it had not been given.
    await expect(due).toHaveValue('')

    await setDueDate(page, 'ticket-due-date', DUE, /\/tickets\//)
    await expect(due).toHaveValue(DUE)

    await page.reload()
    await expect(page.getByTestId('ticket-due-date')).toHaveValue(DUE, { timeout: 10000 })
    await assertNoErrors(page)
  })

  test('a due date cleared on a ticket stays cleared across a reload', async ({ page }) => {
    // The other half of the three states. Clearing sends an explicit null; the
    // naive wiring sends nothing at all, which the server reads as "leave it
    // alone" — so the date would reappear on reload and only on reload.
    await createUserAndLogin(page)
    await createSpace(page, 'Due Date Clear Desk', 'beacon')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Ticket Losing Its Deadline')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Ticket Losing Its Deadline')).toBeVisible({ timeout: 5000 })

    await page.click('text=Ticket Losing Its Deadline')
    await expect(page).toHaveURL(/\/tickets\//, { timeout: 5000 })

    await setDueDate(page, 'ticket-due-date', DUE, /\/tickets\//)
    await page.reload()
    await expect(page.getByTestId('ticket-due-date')).toHaveValue(DUE, { timeout: 10000 })

    await setDueDate(page, 'ticket-due-date', '', /\/tickets\//)
    await page.reload()
    await expect(page.getByTestId('ticket-due-date')).toHaveValue('', { timeout: 10000 })
    await assertNoErrors(page)
  })

  test('setting a ticket due date does not blank the rest of the ticket', async ({ page }) => {
    // The ticket PATCH used to assign every field unconditionally, so a body
    // carrying only due_at meant an empty title. The title is rendered from the
    // server's own response, so this fails loudly against that handler.
    await createUserAndLogin(page)
    await createSpace(page, 'Due Date Partial Desk', 'beacon')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Ticket Keeping Its Title')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Ticket Keeping Its Title')).toBeVisible({ timeout: 5000 })

    await page.click('text=Ticket Keeping Its Title')
    await expect(page).toHaveURL(/\/tickets\//, { timeout: 5000 })

    await setDueDate(page, 'ticket-due-date', DUE, /\/tickets\//)
    await page.reload()

    await expect(page.getByTestId('ticket-due-date')).toHaveValue(DUE, { timeout: 10000 })
    await expect(page.locator('h1:has-text("Ticket Keeping Its Title")')).toBeVisible()
    await assertNoErrors(page)
  })

  test('a due date set on a project item persists across a reload', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Due Date Project', 'vector')

    await page.click('button:has-text("Create Item")')
    await page.fill('#item-title', 'Item With A Deadline')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()
    await expect(page.locator('text=Item With A Deadline')).toBeVisible({ timeout: 5000 })

    await page.click('text=Item With A Deadline')
    await expect(page).toHaveURL(/\/backlog\//, { timeout: 5000 })

    const due = page.getByTestId('item-due-date')
    await expect(due).toBeVisible()
    await expect(due).toHaveValue('')

    await setDueDate(page, 'item-due-date', DUE, /\/projects\/items\//)
    await expect(due).toHaveValue(DUE)

    await page.reload()
    await expect(page.getByTestId('item-due-date')).toHaveValue(DUE, { timeout: 10000 })
    // The item PATCH already handled due_at correctly; what had never existed
    // was a caller. Renaming afterwards is the shape that used to wipe the
    // column, so it is worth one more reload.
    await expect(page.locator('h1:has-text("Item With A Deadline")')).toBeVisible()
    await assertNoErrors(page)
  })
})
