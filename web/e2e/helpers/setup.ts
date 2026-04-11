import { Page, expect } from '@playwright/test'
import { execSync } from 'child_process'

const BINARY = process.env.AZIMUTHAL_BINARY || '/tmp/azimuthal-test'

/**
 * Creates a fresh user via CLI and logs in via the UI.
 * Returns the email, password, and org slug for use in tests.
 * Each call creates a unique user so tests are fully isolated.
 */
export async function createUserAndLogin(page: Page): Promise<{
  email: string
  password: string
}> {
  const ts = Date.now()
  const email = `e2e-${ts}@azimuthal.dev`
  const password = 'E2eTestPass123!'

  // Create user via admin CLI — only supported first-user flow
  try {
    execSync(
      `${BINARY} admin create-user --email "${email}" --name "E2E User" --password "${password}"`,
      { stdio: 'pipe' }
    )
  } catch (err) {
    throw new Error(`Failed to create test user: ${err}`)
  }

  // Navigate to login
  await page.goto('/login')
  await expect(page.locator('input[type="email"]')).toBeVisible({ timeout: 10000 })

  // Fill credentials
  await page.fill('input[type="email"]', email)
  await page.fill('input[type="password"]', password)
  await page.click('button[type="submit"], button:has-text("Sign in")')

  // Wait for successful redirect away from login
  await expect(page).not.toHaveURL(/\/login/, { timeout: 15000 })

  // Verify token was stored
  const token = await page.evaluate((): string | null => {
    for (const key of Object.keys(localStorage)) {
      const val = localStorage.getItem(key)
      if (val && val.startsWith('eyJ')) return val
    }
    return null
  })
  if (!token) throw new Error('Login succeeded but no JWT found in localStorage')

  return { email, password }
}

/**
 * Creates a space of the given type and waits for navigation into it.
 * Returns the space ID extracted from the URL.
 */
export async function createSpace(
  page: Page,
  name: string,
  type: 'service_desk' | 'wiki' | 'project'
): Promise<string> {
  // Open modal
  await page.click('button:has-text("Create Space")')
  await expect(page.locator('text=Create a new space')).toBeVisible({ timeout: 5000 })

  // Fill name
  await page.fill('input[name="name"], input[placeholder*="name"]', name)

  // Select type card
  const typeLabel = {
    service_desk: 'Service Desk',
    wiki: 'Wiki',
    project: 'Project',
  }[type]
  await page.click(`text=${typeLabel}`)

  // Submit
  await page.click('button:has-text("Create Space")')

  // Wait for redirect into the space
  await expect(page).toHaveURL(/\/spaces\//, { timeout: 15000 })

  // Extract and return space ID
  const match = page.url().match(/\/spaces\/([^/]+)/)
  return match ? match[1] : ''
}

/**
 * Gets the auth token from localStorage.
 * Use this to make direct API calls in tests that need to verify
 * backend state after a UI action.
 */
export async function getAuthToken(page: Page): Promise<string> {
  const token = await page.evaluate((): string | null => {
    for (const key of Object.keys(localStorage)) {
      const val = localStorage.getItem(key)
      if (val && val.startsWith('eyJ')) return val
    }
    return null
  })
  if (!token) throw new Error('No auth token found in localStorage')
  return token
}

/**
 * Asserts no error states are visible on the current page.
 * Call this after any navigation to verify the page loaded correctly.
 */
export async function assertNoErrors(page: Page): Promise<void> {
  await expect(page.locator('text=Something went wrong')).not.toBeVisible()
  await expect(page.locator('text=Failed to load')).not.toBeVisible()
  await expect(page.locator('text=invalid space_id')).not.toBeVisible()
  await expect(page.locator('text=invalid request body')).not.toBeVisible()
  await expect(page.locator('text=UNAUTHORIZED')).not.toBeVisible()
}
