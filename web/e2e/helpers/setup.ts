import { Page, expect } from '@playwright/test'
import { execSync } from 'child_process'

/**
 * The server binary the CLI seeding below shells out to. Exported because
 * three spec files used to declare their own copy of this line (T5).
 */
export const BINARY = process.env.AZIMUTHAL_BINARY || '/tmp/azimuthal-test'

/** A user seeded through the admin CLI, with the credentials to sign in as them. */
export interface SeededUser {
  email: string
  password: string
  /** The display name — see seedUser for why this is load-bearing. */
  displayName: string
}

export interface SeedOptions {
  /**
   * The display name, which is ALSO the organization key: the CLI slugifies it
   * and reuses any org whose slug matches, creating one only if none does. So
   * two users seeded with different display names land in different orgs and
   * cannot see each other, and "normalising" this to a single default would
   * silently rewrite what a test is testing. Defaults to 'E2E User', the
   * shared org almost every spec expects.
   */
  displayName?: string
  /**
   * Org membership role. Omitted means the CLI's own default, which is owner —
   * an org admin. Pass 'member' for the non-admin personas.
   */
  role?: 'owner' | 'admin' | 'member'
  /** A short tag in the generated address, so a failure names the persona. */
  tag?: string
}

/**
 * uniqueEmail generates a per-run address.
 *
 * Timestamp AND random suffix: parallel workers do hit the same millisecond,
 * and a bare timestamp then collides on the users_org_id_email_key unique
 * constraint. Exported for the tests that need an address without a user
 * behind it — an invitee, for instance.
 */
export function uniqueEmail(tag?: string): string {
  const prefix = tag ? `${tag}-` : ''
  return `e2e-${prefix}${Date.now()}-${Math.random().toString(36).slice(2, 8)}@azimuthal.dev`
}

/**
 * seedUser creates a user through the admin CLI and returns their credentials.
 */
export function seedUser(opts: SeedOptions = {}): SeededUser {
  const displayName = opts.displayName ?? 'E2E User'
  const email = uniqueEmail(opts.tag)
  const password = 'E2eTestPass123!'
  const role = opts.role ? ` --role ${opts.role}` : ''

  try {
    execSync(
      `${BINARY} admin create-user --email "${email}" --name "${displayName}" --password "${password}"${role}`,
      { stdio: 'pipe' },
    )
  } catch (err) {
    throw new Error(`Failed to seed test user: ${err}`)
  }
  return { email, password, displayName }
}

/**
 * signIn drives the login form and asserts NOTHING about the outcome.
 *
 * That is the whole point of it existing separately from loginAs. A negative
 * login — admin.spec's deactivated member, who must be refused — needs the same
 * five interactions and the opposite expectation, and a helper that asserted
 * success would be unusable there. Splitting the mechanics from the contract is
 * what lets both cases share one implementation instead of one of them keeping
 * a hand-written copy that can drift.
 */
export async function signIn(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/login')
  await expect(page.locator('input[type="email"]')).toBeVisible({ timeout: 10000 })
  await page.fill('input[type="email"]', email)
  await page.fill('input[type="password"]', password)
  await page.click('button[type="submit"], button:has-text("Sign in")')
}

/**
 * loginAs signs in and asserts the login SUCCEEDED: the app navigated away from
 * /login, and a JWT reached localStorage.
 *
 * The token assertion is not redundant with the URL one. LoginPage calls
 * `login(...)` — which writes localStorage synchronously — before `navigate()`,
 * so a session that redirected without a stored token would be a real defect in
 * that ordering, and the URL check alone would not see it. Every later helper
 * here reads the token, so a test that lost it would otherwise fail somewhere
 * unrelated and much less legibly.
 */
export async function loginAs(page: Page, user: SeededUser): Promise<SeededUser> {
  await signIn(page, user.email, user.password)
  await expect(page).not.toHaveURL(/\/login/, { timeout: 15000 })

  const token = await page.evaluate((): string | null =>
    localStorage.getItem('azimuthal_access_token'),
  )
  if (!token) throw new Error('Login succeeded but no JWT found in localStorage')
  return user
}

/**
 * Creates a fresh org-owner user via the CLI and logs them in through the UI.
 * Each call creates a unique user so tests are fully isolated.
 */
export async function createUserAndLogin(page: Page): Promise<SeededUser> {
  return loginAs(page, seedUser())
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
 * Gets the PORTAL session token this browser actually stored, for one portal.
 *
 * The mirror of getAuthToken, and deliberately a separate function rather than
 * a parameter on it: the two are different credential families signed with the
 * same key, and the whole boundary is the `aud` claim
 * (internal/core/portal/token.go). A helper that could return either would be
 * one argument away from handing a portal token to an internal assertion,
 * which is precisely the confusion the split storage keys exist to prevent.
 *
 * It reads localStorage rather than re-issuing a link through the API, because
 * the value under test is the token the REDEEM PAGE STORED. A Go test can mint
 * a token and prove the internal parser refuses it; it cannot prove that what
 * the browser is holding — after the redeem round trip, the session record and
 * the JSON envelope — is that same refused thing. That half only exists here.
 *
 * The key shape (`azimuthal_portal_session:{portalKey}`) is per-portal because
 * a session is bound to one `pid`: presenting portal A's session to portal B
 * answers 404, not 401. See web/src/lib/portalSession.ts.
 */
export async function getPortalToken(page: Page, portalKey: string): Promise<string> {
  const token = await page.evaluate((key: string): string | null => {
    const raw = localStorage.getItem(`azimuthal_portal_session:${key}`)
    if (!raw) return null
    try {
      return (JSON.parse(raw) as { session_token?: string }).session_token ?? null
    } catch {
      return null
    }
  }, portalKey)
  if (!token) throw new Error(`No portal session token in localStorage for portal ${portalKey}`)
  return token
}

/**
 * Asserts no error states are visible on the current page.
 * Call this after any navigation to verify the page loaded correctly.
 *
 * The interior restyle routed every load-failure message through
 * friendlyErrorMessage, whose fallbacks read "… could not be loaded." —
 * without that pattern here, the "Failed to load" check would pass
 * vacuously against pages that can no longer render that string.
 */
export async function assertNoErrors(page: Page): Promise<void> {
  await expect(page.locator('text=Something went wrong')).not.toBeVisible()
  await expect(page.locator('text=Failed to load')).not.toBeVisible()
  await expect(page.locator('text=could not be loaded')).not.toBeVisible()
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
