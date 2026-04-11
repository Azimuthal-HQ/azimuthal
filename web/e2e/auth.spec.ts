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
    await expect(page.locator('text=Azimuthal')).toBeVisible()
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

  test('logout clears session and redirects to login', async ({ page }) => {
    await createUserAndLogin(page)

    // Find and click the user menu
    await page.click('[data-testid="user-menu"], button:has-text("U"), [aria-label*="user"], .user-avatar')
    await page.click('text=Logout')

    await expect(page).toHaveURL(/\/login/, { timeout: 5000 })

    // Token must be cleared
    const token = await page.evaluate((): string | null => {
      for (const key of Object.keys(localStorage)) {
        const val = localStorage.getItem(key)
        if (val && val.startsWith('eyJ')) return val
      }
      return null
    })
    expect(token).toBeNull()
  })

  test('after logout, navigating to / redirects to login', async ({ page }) => {
    await createUserAndLogin(page)
    await page.click('[data-testid="user-menu"], button:has-text("U"), [aria-label*="user"], .user-avatar')
    await page.click('text=Logout')
    await expect(page).toHaveURL(/\/login/)

    await page.goto('/')
    await expect(page).toHaveURL(/\/login/)
  })
})
