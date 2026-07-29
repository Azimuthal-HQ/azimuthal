import { test, expect } from '@playwright/test'
import { createUserAndLogin, seedUser, signIn } from './helpers/setup'

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
    // 'Auth Test' is not decoration: the display name is the org key, so this
    // user gets their own org rather than joining the shared 'E2E User' one.
    const user = seedUser({ displayName: 'Auth Test', tag: 'auth' })

    await signIn(page, user.email, user.password)

    await expect(page).not.toHaveURL(/\/login/, { timeout: 15000 })
    await expect(page.locator('text=Welcome back')).toBeVisible()
  })

  test('login API returns JSON not HTML', async ({ request }) => {
    const response = await request.post('/api/v1/auth/login', {
      data: { email: 'nobody@nowhere.com', password: 'wrong' },
    })
    expect(response.headers()['content-type']).toContain('application/json')
  })

  test('logout clears session and redirects to login', async ({ page }) => {
    // Sign-out lives in the top-bar avatar menu (ADR-0005: account and org
    // concerns live behind the avatar).
    await createUserAndLogin(page)

    await page.getByTestId('avatar-menu').click()
    await page.getByRole('button', { name: 'Sign out', exact: true }).click()

    // Wait for redirect to login or token to be cleared
    await expect(page).toHaveURL(/\/login/, { timeout: 10000 })

    // Token should be cleared after redirect
    const token = await page.evaluate((): string | null =>
      localStorage.getItem('azimuthal_access_token')
    )
    expect(token).toBeNull()
  })

  test('after logout, navigating to / redirects to login', async ({ page }) => {
    // Sign-out lives in the top-bar avatar menu (ADR-0005).
    await createUserAndLogin(page)

    await page.getByTestId('avatar-menu').click()
    await page.getByRole('button', { name: 'Sign out', exact: true }).click()

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
