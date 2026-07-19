import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors } from './helpers/setup'

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
    // frontend expecting 'planning', the button never rendered.
    const startButton = page.getByRole('button', { name: 'Start' })
    await expect(startButton).toBeVisible()

    // And the transition works end-to-end: planned → active.
    await startButton.click()
    await expect(page.getByRole('button', { name: 'Complete' })).toBeVisible({ timeout: 5000 })
    await assertNoErrors(page)
  })
})
