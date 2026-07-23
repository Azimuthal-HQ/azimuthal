import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace } from './helpers/setup'

// S3: the top bar's Create button is module-contextual. Inside a Beacon space
// its primary action opens the New Ticket dialog (before S3 it always opened
// the create-space flow).
test.describe('Contextual Create — S3', () => {
  test('primary Create inside a Beacon space opens the New Ticket dialog', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Contextual Create Desk', 'beacon')

    await page.getByTestId('topbar-create').click()

    // Lands on ?create=ticket, which opens the New Ticket dialog.
    await expect(page.locator('#ticket-title')).toBeVisible({ timeout: 5000 })
    await expect(page).toHaveURL(/\/beacon\/.+\/tickets/)
  })
})
