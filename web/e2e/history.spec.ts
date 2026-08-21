import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors } from './helpers/setup'

// The maintainer's acceptance condition for D5, as a journey: close a ticket,
// reopen it, open the History view, and see BOTH actions. History is a sibling
// of Activity (comments), reached by a toggle — the two never interleave.

test.describe('Ticket History', () => {
  test('a closed-then-reopened ticket shows both actions in History', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'History Journey Desk', 'beacon')

    // A ticket to move through the lifecycle.
    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Lifecycle Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Lifecycle Ticket')).toBeVisible({ timeout: 5000 })

    await page.click('text=Lifecycle Ticket')
    await expect(page).toHaveURL(/\/tickets\//, { timeout: 5000 })

    // Drive it forward and then CLOSE it: open -> in_progress -> closed. The
    // status control reverts on a refused move, so waiting for the value to
    // settle is also the wait for the transition to have persisted.
    const statusSelect = page.getByLabel('Change status')
    await statusSelect.selectOption('in_progress')
    await expect(statusSelect).toHaveValue('in_progress')
    await statusSelect.selectOption('closed')
    await expect(statusSelect).toHaveValue('closed')

    // REOPEN it: closed -> open.
    await statusSelect.selectOption('open')
    await expect(statusSelect).toHaveValue('open')

    // Reload and open the ticket again — the "come back later and check what
    // happened" path, and a clean read of the persisted history.
    await page.reload()
    await expect(page.getByLabel('Change status')).toHaveValue('open')

    // Switch from Activity to History — the sibling feed.
    await page.getByTestId('activity-history-toggle').getByRole('radio', { name: 'History' }).click()

    const history = page.getByTestId('history-list')
    await expect(history).toBeVisible()

    // Three status changes were made (in_progress, closed, open), so three
    // transitions are recorded — the close and the reopen among them.
    await expect(history.getByTestId('history-status-transition')).toHaveCount(3)

    // Both the maintainer's actions are visible: a change into Closed (the
    // close) and a change into Open (the reopen).
    await expect(history.getByText('Closed').first()).toBeVisible()
    await expect(history.getByText('Open').first()).toBeVisible()

    // The two feeds do not interleave: the comment composer is not on screen
    // while History is showing.
    await expect(page.getByTestId('comment-composer')).toBeHidden()

    await assertNoErrors(page)
  })
})
