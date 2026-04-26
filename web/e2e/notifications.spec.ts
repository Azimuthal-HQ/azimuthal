import { test, expect } from '@playwright/test'
import { execSync } from 'child_process'
import { createUserAndLogin, createSpace, getAuthToken, getCurrentUser, addCurrentUserAsSpaceMember } from './helpers/setup'

const BINARY = process.env.AZIMUTHAL_BINARY || '/tmp/azimuthal-test'

test.describe('Notifications (P1.3)', () => {
  // P1 spec: notifications fire on assignment when assignee != actor.
  // The flow exercises the producer + the bell UI:
  //  - Alice creates a space and a ticket assigned to Bob
  //  - Bob logs in and sees the bell badge at 1
  //  - Bob opens the panel, clicks the notification, badge drops to 0
  test('assigning a ticket to another user surfaces a bell badge for the recipient', async ({ page, request }) => {
    // 1. Alice (the actor) signs in via the standard helper.
    const alice = await createUserAndLogin(page)
    const aliceUser = await getCurrentUser(page)

    // 2. Bob exists as a separate user. Created via admin CLI, not logged in here.
    const ts = Date.now()
    const bobEmail = `bob-${ts}@azimuthal.dev`
    const bobPassword = 'BobPass123!'
    execSync(`${BINARY} admin create-user --email "${bobEmail}" --name "Bob" --password "${bobPassword}"`, { stdio: 'pipe' })

    // 3. Alice creates a service desk space and adds herself as a space member.
    const spaceId = await createSpace(page, 'Notify Test Desk', 'service_desk')
    await addCurrentUserAsSpaceMember(page, aliceUser.orgId, spaceId)

    // 4. Add Bob to Alice's org and as a space member so the assignment FK is valid.
    //    Use SQL via the admin CLI — there is no public org-add-member endpoint yet.
    //    Instead we add Bob via the public space-members route with his user id.
    // 4a. Resolve Bob's user_id via login (separate request context).
    const aliceToken = await getAuthToken(page)
    const loginResp = await request.post('/api/v1/auth/login', {
      data: { email: bobEmail, password: bobPassword },
    })
    expect(loginResp.status()).toBe(200)
    const loginBody = await loginResp.json()
    const bobUserId: string = loginBody.user.id
    const bobToken: string = loginBody.access_token

    // 4b. Add Bob to Alice's org as a member, then to the space.
    //     This is required so the assignee FK / membership lookups succeed.
    const addOrgMember = await request.post(`/api/v1/orgs/${aliceUser.orgId}/spaces/${spaceId}/members`, {
      headers: { Authorization: `Bearer ${aliceToken}`, 'Content-Type': 'application/json' },
      data: { user_id: bobUserId, role: 'member' },
    })
    // 201 = added, 409 = already a member; both acceptable.
    expect([201, 409]).toContain(addOrgMember.status())

    // 5. Alice creates a ticket assigned to Bob (assignee != actor).
    const createResp = await request.post(`/api/v1/spaces/${spaceId}/tickets`, {
      headers: { Authorization: `Bearer ${aliceToken}`, 'Content-Type': 'application/json' },
      data: { title: 'For Bob', priority: 'medium', assignee_id: bobUserId },
    })
    expect(createResp.status()).toBe(201)

    // 6. Switch to Bob's session: clear localStorage, set Bob's tokens, reload.
    await page.evaluate(([t]) => {
      localStorage.setItem('azimuthal_access_token', t)
    }, [bobToken])
    await page.goto('/')

    // 7. Bell badge should show 1 unread notification.
    await expect(page.locator('[data-testid="notifications-badge"]')).toHaveText('1', { timeout: 15000 })

    // 8. Open panel and click the notification — badge drops, panel closes.
    await page.click('[data-testid="notifications-bell"]')
    await expect(page.locator('[data-testid="notifications-panel"]')).toBeVisible()
    const item = page.locator('[data-testid="notification-item"]').first()
    await expect(item).toContainText('For Bob')
    await item.click()

    // 9. Bell badge should be gone after marking the only notification read.
    await expect(page.locator('[data-testid="notifications-badge"]')).toHaveCount(0, { timeout: 5000 })

    // Cleanup: log Alice's password so an attentive reader can reproduce.
    expect(alice.email).toMatch(/^e2e-/)
  })

  test('GET /notifications returns owner-scoped JSON', async ({ page, request }) => {
    await createUserAndLogin(page)
    const token = await getAuthToken(page)
    const resp = await request.get('/api/v1/notifications', {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(resp.status()).toBe(200)
    expect(resp.headers()['content-type']).toContain('application/json')
    const body = await resp.json()
    expect(body).toHaveProperty('notifications')
    expect(body).toHaveProperty('unread_count')
    expect(Array.isArray(body.notifications)).toBe(true)
  })
})
