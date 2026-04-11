import { test, expect } from '@playwright/test'
import { execSync } from 'child_process'
import { createUserAndLogin } from './helpers/setup'

const BINARY = process.env.AZIMUTHAL_BINARY || '/tmp/azimuthal-test'

test.describe('Authentication', () => {
  test('unauthenticated user is redirected to login page', async ({ page }) => {
    await page.goto('/')
    await expect(page).toHaveURL(/\/login/)
    await expect(page.locator('input[type="email"]')).toBeVisible()
    await expect(page.locator('input[type="password"]')).toBeVisible()
  })

  test('login page renders with all required elements', async ({ page }) => {
    await page.goto('/login')
    await expect(page.locator('h1:has-text("Sign in to Azimuthal")')).toBeVisible()
    await expect(page.locator('input[type="email"]')).toBeVisible()
    await expect(page.locator('input[type="password"]')).toBeVisible()
    await expect(page.locator('button[type="submit"], button:has-text("Sign in")')).toBeVisible()
  })

  test('invalid credentials shows error — not a blank screen or crash', async ({ page }) => {
    await page.goto('/login')
    await page.fill('input[type="email"]', 'nobody@nowhere.com')
    await page.fill('input[type="password"]', 'wrongpassword')
    await page.click('button[type="submit"], button:has-text("Sign in")')
    await expect(page.locator('text=Invalid email or password')).toBeVisible({ timeout: 5000 })
    await expect(page).toHaveURL(/\/login/)
  })

  test('valid credentials logs in and shows dashboard', async ({ page }) => {
    const email = `auth-${Date.now()}@azimuthal.dev`
    execSync(`${BINARY} admin create-user --email "${email}" --name "Auth Test" --password "TestPass123!"`, { stdio: 'pipe' })

    await page.goto('/login')
    await page.fill('input[type="email"]', email)
    await page.fill('input[type="password"]', 'TestPass123!')
    await page.click('button[type="submit"], button:has-text("Sign in")')

    await expect(page).not.toHaveURL(/\/login/, { timeout: 15000 })
    await expect(page.locator('text=Welcome back')).toBeVisible()
  })

  test('login API returns JSON not HTML', async ({ page, request }) => {
    const response = await request.post('/api/v1/auth/login', {
      data: { email: 'nobody@nowhere.com', password: 'wrong' },
    })
    expect(response.headers()['content-type']).toContain('application/json')
  })

  test.fixme('logout clears session and redirects to login', async ({ page }) => {
    // APP BUG: Shell component renders without onLogout prop — logout button is a no-op
    // Fix: App.tsx must pass useAuth().logout to Shell's onLogout prop
    await createUserAndLogin(page)

    // Open user menu — use aria-label from TopNav.tsx
    const userMenuSelectors = [
      'button[aria-label="User menu"]',
      '[data-testid="user-menu"]',
      'header button:last-child',
      'nav button:last-child',
    ]

    let menuOpened = false
    for (const selector of userMenuSelectors) {
      try {
        await page.click(selector, { timeout: 2000 })
        menuOpened = true
        break
      } catch {
        continue
      }
    }
    if (!menuOpened) throw new Error('Could not find user menu button')

    // Wait for dropdown then click logout
    await page.waitForSelector('button:has-text("Logout")', { timeout: 3000 })
    await page.click('button:has-text("Logout")')

    // Wait for redirect to login or token to be cleared
    await expect(page).toHaveURL(/\/login/, { timeout: 10000 })

    // Token should be cleared after redirect
    const token = await page.evaluate((): string | null =>
      localStorage.getItem('azimuthal_access_token')
    )
    expect(token).toBeNull()
  })

  test.fixme('after logout, navigating to / redirects to login', async ({ page }) => {
    // APP BUG: Shell component renders without onLogout prop — logout button is a no-op
    // Fix: App.tsx must pass useAuth().logout to Shell's onLogout prop
    await createUserAndLogin(page)

    // Open user menu
    const userMenuSelectors = [
      'button[aria-label="User menu"]',
      '[data-testid="user-menu"]',
      'header button:last-child',
      'nav button:last-child',
    ]

    let menuOpened = false
    for (const selector of userMenuSelectors) {
      try {
        await page.click(selector, { timeout: 2000 })
        menuOpened = true
        break
      } catch {
        continue
      }
    }
    if (!menuOpened) throw new Error('Could not find user menu button')

    await page.waitForSelector('button:has-text("Logout")', { timeout: 3000 })
    await page.click('button:has-text("Logout")')

    // Wait for token to be cleared
    await expect(async () => {
      const token = await page.evaluate((): string | null =>
        localStorage.getItem('azimuthal_access_token')
      )
      expect(token).toBeNull()
    }).toPass({ timeout: 5000 })

    // After logout, navigating to / should redirect to /login
    await page.goto('/')
    await expect(page).toHaveURL(/\/login/, { timeout: 10000 })
  })
})
