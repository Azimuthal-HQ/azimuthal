import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace } from './helpers/setup'

test.describe('Notifications — P1', () => {
  test('bell badge increments after self-assignment and clears after mark-all-read', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Notification Test Desk', 'service_desk')

    // Create a ticket
    await page.click('button:has-text("New Ticket")')
    await expect(page.locator('#ticket-title')).toBeVisible()
    await page.fill('#ticket-title', 'Assign Notification Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Assign Notification Ticket')).toBeVisible({ timeout: 5000 })

    // Open ticket detail
    await page.click('text=Assign Notification Ticket')
    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })
    await expect(page).toHaveURL(/\/tickets\//, { timeout: 5000 })

    // Assignee dropdown must show at least one member (creator is auto-added).
    // NOTE: <option> elements inside a closed native <select> are never
    // "visible" to Playwright — assert on count, as service-desk.spec does.
    const assigneeSelect = page.locator('select').filter({ hasText: 'Unassigned' }).first()
    await expect(assigneeSelect).toBeVisible({ timeout: 5000 })
    const memberOptions = assigneeSelect.locator('option:not([value=""])')
    await expect(memberOptions).not.toHaveCount(0, { timeout: 5000 })

    // Assign to self — pick the first (and only) member
    const memberValue = await memberOptions.first().getAttribute('value')
    await assigneeSelect.selectOption(memberValue ?? '')

    // Wait for the notification job to process (River processes async)
    await page.waitForTimeout(3000)

    // Reload to pick up fresh notification count (bell polls every 30s, so reload is faster)
    await page.reload()
    await expect(page).not.toHaveURL(/\/login/)

    // Bell badge must show at least 1 unread notification
    const badge = page.locator('header').locator('span').filter({ hasText: /^[1-9]/ }).first()
    await expect(badge).toBeVisible({ timeout: 10000 })

    // Click the bell to open the panel
    await page.locator('header button[aria-label="Notifications"]').click()

    // Notification panel must appear with at least one item
    const panel = page.locator('header').getByText('Notifications').first().locator('..').locator('..')
    await expect(page.locator('text=You have been assigned')).toBeVisible({ timeout: 5000 })

    // Click "Mark all read"
    await page.locator('button:has-text("Mark all read")').click()

    // Wait for state to update
    await page.waitForTimeout(1000)

    // Badge must disappear after marking all read
    await expect(badge).not.toBeVisible({ timeout: 5000 })
  })

  test('notification bell starts at zero on fresh login', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Zero Bell Test', 'service_desk')

    // No assignments yet — badge must not be visible
    const badge = page.locator('header').locator('span').filter({ hasText: /^[1-9]/ }).first()
    await expect(badge).not.toBeVisible()
  })

  test('clicking bell opens and closes notification panel', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Bell Toggle Test', 'service_desk')

    const bellBtn = page.locator('header button[aria-label="Notifications"]')
    await expect(bellBtn).toBeVisible()

    // Open panel
    await bellBtn.click()
    await expect(page.locator('text=Notifications').first()).toBeVisible({ timeout: 3000 })

    // Close by clicking elsewhere
    await page.locator('header').locator('text=Azimuthal').click()
    await expect(page.locator('button:has-text("Mark all read")')).not.toBeVisible({ timeout: 3000 })
  })

  test('GET /api/v1/notifications returns correct shape', async ({ page }) => {
    await createUserAndLogin(page)
    const token = await page.evaluate((): string | null =>
      localStorage.getItem('azimuthal_access_token')
    )

    const response = await page.request.get('/api/v1/notifications', {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(response.status()).toBe(200)

    const body = await response.json()
    expect(body).toHaveProperty('notifications')
    expect(body).toHaveProperty('unread_count')
    expect(Array.isArray(body.notifications)).toBe(true)
    expect(typeof body.unread_count).toBe('number')
  })
})
