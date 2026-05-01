import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors } from './helpers/setup'

test.describe('Navigation', () => {
  test('navigating between all three module types shows no blank screens', async ({ page }) => {
    await createUserAndLogin(page)

    await createSpace(page, 'Nav SD', 'service_desk')
    await assertNoErrors(page)
    await page.click('text=Back to Dashboard')

    await createSpace(page, 'Nav Wiki', 'wiki')
    await assertNoErrors(page)
    await page.click('text=Back to Dashboard')

    await createSpace(page, 'Nav Proj', 'project')
    await assertNoErrors(page)
    await page.click('text=Back to Dashboard')

    await expect(page).toHaveURL('/')
    await expect(page.locator('text=Welcome back')).toBeVisible()
  })

  test('sidebar does not duplicate when navigating within a module', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Sidebar Stability Test', 'service_desk')

    // Navigate between views inside the module multiple times
    await page.click('text=Kanban Board')
    await page.click('text=Tickets')
    await page.click('text=Kanban Board')
    await page.click('text=Tickets')

    // Count nav items — duplication bug produced 50+ items
    const navItems = await page.locator('nav a, aside a, [role="navigation"] a').count()
    expect(navItems).toBeLessThan(20)
  })

  test('direct URL navigation always serves the app', async ({ page }) => {
    await createUserAndLogin(page)
    const routes = ['/settings']
    for (const route of routes) {
      await page.goto(route)
      const body = await page.locator('body').textContent()
      expect(body?.trim().length).toBeGreaterThan(10)
      await expect(page.locator('text=Something went wrong')).not.toBeVisible()
    }
  })

  test('dark mode persists across page refresh', async ({ page }) => {
    await createUserAndLogin(page)

    const getTheme = (): Promise<boolean> => page.evaluate((): boolean =>
      document.documentElement.classList.contains('dark') ||
      document.documentElement.getAttribute('data-theme') === 'dark'
    )

    // Toggle to light mode
    const initial = await getTheme()
    await page.click('[data-testid="theme-toggle"], button[aria-label*="theme"], button[aria-label*="mode"], .theme-toggle')
    const toggled = await getTheme()
    expect(toggled).toBe(!initial)

    // Reload and verify it persisted
    await page.reload()
    const afterReload = await getTheme()
    expect(afterReload).toBe(toggled)
  })

  test('API routes return JSON — never HTML', async ({ page, request }) => {
    // These must always be JSON regardless of auth state
    const jsonRoutes = ['/health', '/api/v1/health']
    for (const route of jsonRoutes) {
      const response = await request.get(route)
      const ct = response.headers()['content-type'] ?? ''
      expect(ct, `${route} returned non-JSON content-type`).toContain('application/json')
    }
  })

  test('frontend routes return HTML — SPA fallback working', async ({ request }) => {
    const htmlRoutes = ['/', '/login', '/settings']
    for (const route of htmlRoutes) {
      const response = await request.get(route)
      const ct = response.headers()['content-type'] ?? ''
      expect(ct, `${route} should return text/html`).toContain('text/html')
    }
  })
})
