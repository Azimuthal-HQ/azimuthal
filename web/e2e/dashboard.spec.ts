import { test, expect } from '@playwright/test'
import { createUserAndLogin, assertNoErrors, getAuthToken } from './helpers/setup'

test.describe('Dashboard', () => {
  test('shows welcome message for new user', async ({ page }) => {
    await createUserAndLogin(page)
    await expect(page.locator('text=Welcome back')).toBeVisible()
    await expect(page.locator('button:has-text("Create Space")')).toBeVisible()
  })

  test('no error states visible on fresh dashboard', async ({ page }) => {
    await createUserAndLogin(page)
    await assertNoErrors(page)
  })

  test('no console errors on dashboard load', async ({ page }) => {
    const errors: string[] = []
    page.on('console', msg => {
      if (msg.type() === 'error') errors.push(msg.text())
    })
    await createUserAndLogin(page)
    // Filter known non-app errors (browser extensions etc)
    const appErrors = errors.filter(e =>
      !e.includes('favicon') &&
      !e.includes('extension') &&
      !e.includes('chrome-extension')
    )
    expect(appErrors).toHaveLength(0)
  })

  test('API calls return JSON not HTML', async ({ page }) => {
    await createUserAndLogin(page)
    const token = await getAuthToken(page)
    const response = await page.request.get('/api/v1/me', {
      headers: { Authorization: `Bearer ${token}` }
    })
    expect(response.status()).toBe(200)
    expect(response.headers()['content-type']).toContain('application/json')
  })

  test('health endpoint returns JSON', async ({ request }) => {
    const response = await request.get('/health')
    expect(response.status()).toBe(200)
    expect(response.headers()['content-type']).toContain('application/json')
    const body = await response.json()
    expect(body.status).toBe('ok')
  })

  test('dark mode is the default', async ({ page }) => {
    await createUserAndLogin(page)
    const isDark = await page.evaluate((): boolean =>
      document.documentElement.classList.contains('dark') ||
      document.documentElement.getAttribute('data-theme') === 'dark'
    )
    expect(isDark).toBe(true)
  })

  test('dark mode toggle switches theme', async ({ page }) => {
    await createUserAndLogin(page)
    const getTheme = (): Promise<boolean> => page.evaluate((): boolean =>
      document.documentElement.classList.contains('dark') ||
      document.documentElement.getAttribute('data-theme') === 'dark'
    )
    const before = await getTheme()
    await page.click('[data-testid="theme-toggle"], button[aria-label*="theme"], button[aria-label*="mode"], .theme-toggle')
    const after = await getTheme()
    expect(after).toBe(!before)
  })

  test('direct URL to /settings serves the app not a 404', async ({ page }) => {
    await createUserAndLogin(page)
    await page.goto('/settings')
    await expect(page.locator('text=Settings')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=Something went wrong')).not.toBeVisible()
  })

  test('SPA routes never return a blank page', async ({ page }) => {
    await createUserAndLogin(page)
    const routes = ['/', '/settings']
    for (const route of routes) {
      await page.goto(route)
      const bodyText = await page.locator('body').textContent()
      expect(bodyText?.trim().length).toBeGreaterThan(10)
      await expect(page.locator('text=Something went wrong')).not.toBeVisible()
    }
  })
})
