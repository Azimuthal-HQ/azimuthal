import { test, expect } from '@playwright/test'
import { createUserAndLogin, getAuthToken, getCurrentUser, loginAs, seedUser, signIn, uniqueEmail, type SeededUser } from './helpers/setup'

// P2.5 administration journeys. The CLI seeds every e2e user into the shared
// "e2e-user" org (keyed off the display name), so every assertion is scoped
// to per-run unique names and ids — never bare counts.

/** Creates a non-admin member of the shared org via the CLI. */
function createMemberViaCLI(): SeededUser {
  return seedUser({ role: 'member', tag: 'member' })
}

/** API helpers over the admin surface, using the page's stored token. */
async function apiPost(page: import('@playwright/test').Page, path: string, data: unknown) {
  const token = await getAuthToken(page)
  return page.request.post(path, {
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    data,
  })
}

async function createTeamViaAPI(page: import('@playwright/test').Page, name: string): Promise<string> {
  const { orgId } = await getCurrentUser(page)
  const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, '-')
  const res = await apiPost(page, `/api/v1/orgs/${orgId}/teams`, { name, slug })
  if (res.status() !== 201) throw new Error(`create team: ${res.status()} ${await res.text()}`)
  const team = (await res.json()) as { id: string }
  return team.id
}

async function createSpaceFullViaAPI(page: import('@playwright/test').Page, name: string, type: string): Promise<{ id: string; slug: string }> {
  const { orgId } = await getCurrentUser(page)
  const ts = Date.now()
  const uniqueName = `${name} ${ts}`
  const slug = uniqueName.toLowerCase().replace(/[^a-z0-9]+/g, '-')
  const key = ('E2' + Math.random().toString(36).replace(/[^a-z0-9]/g, '').toUpperCase()).slice(0, 8)
  const res = await apiPost(page, `/api/v1/orgs/${orgId}/spaces`, { name: uniqueName, slug, key, type })
  if (res.status() !== 201) throw new Error(`create space: ${res.status()} ${await res.text()}`)
  const space = (await res.json()) as { id: string }
  return { id: space.id, slug }
}

async function createSpaceViaAPI(page: import('@playwright/test').Page, name: string, type: string): Promise<string> {
  const { id } = await createSpaceFullViaAPI(page, name, type)
  return id
}

test.describe('Administration area', () => {
  test('is invisible to non-admins: no menu entry, 404 page, 404 API', async ({ page }) => {
    await loginAs(page, createMemberViaCLI())

    // No Administration entry in the avatar menu.
    await page.getByTestId('avatar-menu').click()
    await expect(page.getByText('Profile')).toBeVisible()
    await expect(page.getByTestId('avatar-menu-admin')).toHaveCount(0)
    await page.keyboard.press('Escape')

    // The route renders the branded not-found, not the admin shell.
    await page.goto('/admin/people')
    await expect(page.getByTestId('admin-layout')).toHaveCount(0)
    await expect(page.getByText(/page not found|doesn.t exist|not found/i).first()).toBeVisible()

    // The API answers 404, never 403 — the surface does not exist for them.
    const token = await getAuthToken(page)
    const { orgId } = await getCurrentUser(page)
    const res = await page.request.get(`/api/v1/orgs/${orgId}/users`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(res.status()).toBe(404)
  })

  test('admin invites a person; the invite link creates their account', async ({ page, browser }) => {
    await createUserAndLogin(page)
    const invitedEmail = uniqueEmail('invitee')

    // The avatar menu shows Administration for an org admin.
    await page.getByTestId('avatar-menu').click()
    await page.getByTestId('avatar-menu-admin').click()
    await expect(page.getByTestId('admin-people')).toBeVisible({ timeout: 15000 })

    // Invite through the dialog.
    await page.getByTestId('people-invite-button').click()
    await page.getByTestId('invite-emails').fill(invitedEmail)
    await page.getByTestId('invite-submit').click()

    // The one-time link is shown; capture it.
    const linkNode = page.getByTestId(`invite-link-${invitedEmail}`)
    await expect(linkNode).toBeVisible()
    const inviteUrl = (await linkNode.getAttribute('title')) ?? (await linkNode.textContent()) ?? ''
    expect(inviteUrl).toContain('/invite/')
    await page.getByTestId('invite-done').click()

    // The pending invite is listed.
    await expect(page.getByTestId(`invite-row-${invitedEmail}`)).toBeVisible()

    // A logged-out browser accepts it and lands signed in.
    const ctx = await browser.newContext()
    const invitePage = await ctx.newPage()
    const path = new URL(inviteUrl).pathname
    await invitePage.goto(path)
    await expect(invitePage.getByTestId('invite-accept-page')).toBeVisible({ timeout: 15000 })
    await invitePage.getByTestId('invite-display-name').fill('Invited Person')
    await invitePage.getByTestId('invite-password').fill('InvitedPass123!')
    await invitePage.getByTestId('invite-accept-submit').click()
    await expect(invitePage).toHaveURL(/\/$/, { timeout: 15000 })
    const inviteeToken = await invitePage.evaluate((): string | null =>
      localStorage.getItem('azimuthal_access_token'),
    )
    expect(inviteeToken).toBeTruthy()
    await ctx.close()

    // The person now appears in People, and the pending invite is gone.
    await page.reload()
    await expect(page.getByTestId(`person-row-${invitedEmail}`)).toBeVisible({ timeout: 15000 })
    await expect(page.getByTestId(`invite-row-${invitedEmail}`)).toHaveCount(0)
  })

  test('deactivation kills a live session on its very next request', async ({ page, browser }) => {
    await createUserAndLogin(page)
    const member = createMemberViaCLI()

    // The member signs in and holds a live token.
    const memberCtx = await browser.newContext()
    const memberPage = await memberCtx.newPage()
    await loginAs(memberPage, member)
    const memberToken = await getAuthToken(memberPage)

    // Premise: the token works.
    const before = await memberPage.request.get('/api/v1/auth/me', {
      headers: { Authorization: `Bearer ${memberToken}` },
    })
    expect(before.status()).toBe(200)

    // The admin deactivates them through the People UI.
    await page.goto('/admin/people')
    const row = page.getByTestId(`person-row-${member.email}`)
    await expect(row).toBeVisible({ timeout: 15000 })
    await page.getByTestId(`person-actions-${member.email}`).click()
    await page.getByTestId(`person-deactivate-${member.email}`).click()
    await page.getByTestId('person-confirm-action').click()
    await expect(page.getByTestId(`person-status-${member.email}`)).toHaveText('deactivated', { timeout: 15000 })

    // The token minted BEFORE deactivation dies on its next request.
    const after = await memberPage.request.get('/api/v1/auth/me', {
      headers: { Authorization: `Bearer ${memberToken}` },
    })
    expect(after.status()).toBe(401)

    // Sign-in is blocked too. This one drives the form through signIn and
    // asserts the OPPOSITE of loginAs — a deactivated member must be left on
    // /login. It is the reason signIn exists apart from loginAs.
    await signIn(memberPage, member.email, member.password)
    await expect(memberPage).toHaveURL(/\/login/)
    await memberCtx.close()

    // Reactivation restores sign-in (fresh credentials, fresh token).
    await page.getByTestId(`person-actions-${member.email}`).click()
    await page.getByTestId(`person-reactivate-${member.email}`).click()
    await expect(page.getByTestId(`person-status-${member.email}`)).toHaveText('active', { timeout: 15000 })
  })

  test('matrix: stage a row, preview counts, apply as one audited batch', async ({ page }) => {
    await createUserAndLogin(page)
    const run = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    const teamId = await createTeamViaAPI(page, `Matrix Team ${run}`)
    const spaceA = await createSpaceViaAPI(page, `Matrix ${run} A`, 'vector')
    const spaceB = await createSpaceViaAPI(page, `Matrix ${run} B`, 'codex')

    await page.goto('/admin/access')
    await expect(page.getByTestId('admin-access-matrix')).toBeVisible({ timeout: 15000 })

    // Scope BOTH axes to this run: row-staging targets every VISIBLE
    // space, and the shared e2e org accumulates spaces from other tests.
    await page.getByTestId('matrix-filter-team').fill(`Matrix Team ${run}`)
    await page.getByTestId('matrix-filter-space').fill(`Matrix ${run}`)
    await expect(page.getByTestId(`matrix-cell-${teamId}-${spaceA}`)).toBeVisible({ timeout: 15000 })
    await page.getByTestId(`matrix-row-${teamId}`).click()
    await page.getByTestId('matrix-editor-role-contributor').click()
    await expect(page.getByTestId('matrix-staged-bar')).toBeVisible()

    // Preview: the server-computed diff names two creations.
    await page.getByTestId('matrix-preview-button').click()
    const summary = page.getByTestId('matrix-preview-summary')
    await expect(summary).toBeVisible()
    await expect(summary).toContainText('2 new grants, 0 role changes, 0 revocations')

    // Ticket reference + the second, explicit confirmation.
    await page.getByTestId('matrix-ticket-ref').fill(`E2E-${run}`)
    await page.getByTestId('matrix-apply-button').click()
    await expect(page.getByTestId('matrix-applied-note')).toContainText('2 new grants', { timeout: 15000 })

    // Both cells are now direct grants.
    await expect(page.getByTestId(`matrix-cell-${teamId}-${spaceA}`)).toHaveAttribute('data-state', 'direct')
    await expect(page.getByTestId(`matrix-cell-${teamId}-${spaceB}`)).toHaveAttribute('data-state', 'direct')

    // The audit viewer shows the batch as ONE expandable row carrying the
    // ticket reference, expanding to its two events.
    await page.getByTestId('admin-tab-audit').click()
    await expect(page.getByTestId('admin-audit-log')).toBeVisible({ timeout: 15000 })
    const batchRow = page.locator('[data-testid^="audit-batch-row-"]').filter({ hasText: `E2E-${run}` }).first()
    await expect(batchRow).toBeVisible({ timeout: 15000 })
    await expect(batchRow).toContainText('×2')
    await batchRow.click()
    await expect(page.getByTestId('audit-batch-event')).toHaveCount(2, { timeout: 15000 })
  })

  test('ghosted parent cell creates a direct grant without touching the child grant', async ({ page }) => {
    await createUserAndLogin(page)
    const run = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    const { orgId } = await getCurrentUser(page)
    const parentId = await createTeamViaAPI(page, `Ghost Parent ${run}`)
    const childRes = await apiPost(page, `/api/v1/orgs/${orgId}/teams`, {
      name: `Ghost Child ${run}`,
      slug: `ghost-child-${run}`,
      parent_id: parentId,
    })
    expect(childRes.status()).toBe(201)
    const childId = ((await childRes.json()) as { id: string }).id
    const spaceId = await createSpaceViaAPI(page, 'Ghost Space', 'vector')

    // The child holds the only grant.
    const grantRes = await apiPost(page, `/api/v1/orgs/${orgId}/spaces/${spaceId}/grants`, {
      subject_type: 'team',
      subject_id: childId,
      role: 'viewer',
    })
    expect(grantRes.status()).toBe(201)

    await page.goto('/admin/access')
    await page.getByTestId('matrix-filter-team').fill(`Ghost`)

    // The parent's cell renders ghosted; the child's renders direct.
    const parentCell = page.getByTestId(`matrix-cell-${parentId}-${spaceId}`)
    await expect(parentCell).toHaveAttribute('data-state', 'inherited', { timeout: 15000 })
    await expect(page.getByTestId(`matrix-cell-${childId}-${spaceId}`)).toHaveAttribute('data-state', 'direct')

    // Acting on the ghost offers to CREATE a direct grant — the note names
    // the child as the inherited source.
    await parentCell.click()
    const note = page.getByTestId('matrix-inherited-note')
    await expect(note).toContainText(`inherited from Ghost Child ${run}`)
    await expect(note).toContainText('creates a direct grant')
    await page.getByTestId('matrix-editor-role-agent').click()
    await page.getByTestId('matrix-preview-button').click()
    await expect(page.getByTestId('matrix-preview-summary')).toContainText('1 new grants, 0 role changes, 0 revocations')
    await page.getByTestId('matrix-apply-button').click()
    await expect(page.getByTestId('matrix-applied-note')).toBeVisible({ timeout: 15000 })

    // Both rows now hold DISTINCT direct grants; the child kept viewer.
    await expect(parentCell).toHaveAttribute('data-state', 'direct')
    const grants = await page.request.get(`/api/v1/orgs/${orgId}/spaces/${spaceId}/grants`, {
      headers: { Authorization: `Bearer ${await getAuthToken(page)}` },
    })
    const rows = (await grants.json()) as Array<{ subject_id: string; role: string }>
    const childGrant = rows.find((g) => g.subject_id === childId)
    const parentGrant = rows.find((g) => g.subject_id === parentId)
    expect(childGrant?.role).toBe('viewer')
    expect(parentGrant?.role).toBe('agent')
  })

  test('spaces admin: delete confirmation names the space and counts its contents', async ({ page }) => {
    await createUserAndLogin(page)
    // The run token rides in the name so the row is uniquely this test's —
    // the shared e2e org accumulates spaces from parallel workers.
    const run = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    const { id: spaceId, slug } = await createSpaceFullViaAPI(page, `Doomed ${run}`, 'beacon')
    const { orgId } = await getCurrentUser(page)

    // Give it one ticket so the count is non-trivial.
    const tRes = await apiPost(page, `/api/v1/orgs/${orgId}/spaces/${spaceId}/tickets`, {
      title: 'Lone ticket',
      priority: 'low',
    })
    expect(tRes.status()).toBe(201)

    await page.goto('/admin/spaces')
    await expect(page.getByTestId('admin-spaces')).toBeVisible({ timeout: 15000 })
    await expect(page.getByTestId(`admin-space-row-${slug}`)).toBeVisible({ timeout: 15000 })

    await page.getByTestId(`admin-space-delete-${slug}`).click()
    const summary = page.getByTestId('admin-space-delete-summary')
    await expect(summary).toContainText('1 ticket', { timeout: 15000 })
    await page.getByTestId('admin-space-delete-confirm').click()
    await expect(page.getByTestId(`admin-space-row-${slug}`)).toHaveCount(0, { timeout: 15000 })
  })

  // S9: org settings live in the admin panel — one home for them, the old
  // Home->Settings location redirects, and the slug is display-only.
  test('org settings live in Admin; old path redirects; not under Home Settings', async ({ page }) => {
    await createUserAndLogin(page) // an org admin

    await page.goto('/admin/settings')
    await expect(page.getByTestId('admin-org-settings')).toBeVisible({ timeout: 15000 })
    await expect(page.getByTestId('admin-org-name')).toBeVisible()
    await expect(page.getByTestId('admin-org-slug')).toBeDisabled()
    await expect(page.getByTestId('admin-tab-settings')).toBeVisible()

    // The old location redirects into the admin panel.
    await page.goto('/settings/organization')
    await expect(page).toHaveURL(/\/admin\/settings/, { timeout: 15000 })

    // The Home Settings page no longer offers an Organization tab.
    await page.goto('/settings')
    await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible({ timeout: 15000 })
    await expect(page.getByRole('button', { name: 'Organization' })).toHaveCount(0)
  })
})
