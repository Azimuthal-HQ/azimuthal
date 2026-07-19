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
  // Random suffix: parallel workers can hit the same millisecond, and a bare
  // timestamp then collides on the users_org_id_email_key unique constraint.
  const suffix = Math.random().toString(36).slice(2, 8)
  const email = `e2e-${ts}-${suffix}@azimuthal.dev`
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
  const token = await page.evaluate((): string | null =>
    localStorage.getItem('azimuthal_access_token')
  )
  if (!token) throw new Error('Login succeeded but no JWT found in localStorage')

  return { email, password }
}

/**
 * Creates a space of the given type via direct API call and navigates into it.
 * Returns the space ID.
 *
 * Uses the API directly (not the UI dialog) so tests are decoupled from dialog
 * implementation details, parallel workers never collide on key uniqueness, and
 * failures surface as clear errors rather than silent URL timeouts.
 */
export async function createSpace(
  page: Page,
  name: string,
  type: 'beacon' | 'codex' | 'vector'
): Promise<string> {
  const token = await getAuthToken(page)
  const { orgId } = await getCurrentUser(page)

  const ts = Date.now()
  const uniqueName = `${name} ${ts}`
  const slug = uniqueName.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')

  // Key must match ^[A-Z0-9]{1,10}$ and be unique per org. A random base36
  // suffix keeps parallel workers AND repeated runs from colliding — the last
  // digits of Date.now() cycle every 10s, which produced real 409s across
  // back-to-back runs of the same test.
  const prefix = name.replace(/[^A-Za-z0-9]/g, '').toUpperCase().slice(0, 6) || 'SP'
  const key = prefix + Math.random().toString(36).replace(/[^a-z0-9]/g, '').slice(0, 4).toUpperCase().padEnd(4, '0')

  const response = await page.request.post(
    `/api/v1/orgs/${orgId}/spaces`,
    {
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      data: { name: uniqueName, slug, key, type },
    }
  )

  if (response.status() !== 201) {
    const body = await response.text()
    throw new Error(`createSpace API failed: ${response.status()} ${body}`)
  }

  const space = await response.json() as { id: string }

  const spaceUrl =
    type === 'beacon' ? `/beacon/${space.id}/tickets` :
    type === 'codex'  ? `/codex/${space.id}` :
                        `/vector/${space.id}/backlog`

  await page.goto(spaceUrl)
  await expect(page).toHaveURL(/\/(beacon|codex|vector)\//, { timeout: 15000 })

  return space.id
}

/**
 * Gets the auth token from localStorage.
 * Use this to make direct API calls in tests that need to verify
 * backend state after a UI action.
 */
export async function getAuthToken(page: Page): Promise<string> {
  const token = await page.evaluate((): string | null =>
    localStorage.getItem('azimuthal_access_token')
  )
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

/**
 * Gets the current user's org ID and user ID from the /auth/me endpoint.
 */
export async function getCurrentUser(page: Page): Promise<{ userId: string; orgId: string; displayName: string }> {
  const token = await getAuthToken(page)
  const response = await page.request.get('/api/v1/auth/me', {
    headers: { Authorization: `Bearer ${token}` },
  })
  if (response.status() !== 200) throw new Error(`GET /auth/me returned ${response.status()}`)
  const user = await response.json()
  return { userId: user.id, orgId: user.org_id, displayName: user.display_name }
}

/**
 * Adds the current user as a member of the given space.
 * Space creation auto-adds the creator as an admin member, but this
 * helper remains for tests that create spaces via API (not UI) or need
 * to add a second user as a member.
 */
export async function addCurrentUserAsSpaceMember(page: Page, orgId: string, spaceId: string): Promise<void> {
  const token = await getAuthToken(page)
  const { userId } = await getCurrentUser(page)
  const response = await page.request.post(
    `/api/v1/orgs/${orgId}/spaces/${spaceId}/members`,
    {
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      data: { user_id: userId, role: 'member' },
    },
  )
  if (response.status() !== 201) {
    const body = await response.text()
    throw new Error(`Failed to add space member: ${response.status()} ${body}`)
  }
}
