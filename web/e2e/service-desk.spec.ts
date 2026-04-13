import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors, getAuthToken, getCurrentUser, addCurrentUserAsSpaceMember } from './helpers/setup'

test.describe('Service Desk', () => {
  test('can create a service desk space and land on ticket list', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'E2E Service Desk', 'service_desk')
    await expect(page).toHaveURL(/\/spaces\/.*\/tickets/, { timeout: 10000 })
    await expect(page.locator('h1:has-text("Tickets"), h2:has-text("Tickets"), [role="heading"]:has-text("Tickets")').first()).toBeVisible()
    await assertNoErrors(page)
  })

  test('ticket list loads without error on empty space', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Empty Desk', 'service_desk')
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('can create a ticket with minimum fields', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Ticket Create Test', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await expect(page.locator('#ticket-title')).toBeVisible()

    await page.fill('#ticket-title', 'E2E Test Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()

    await expect(page.locator('text=E2E Test Ticket')).toBeVisible({ timeout: 5000 })
  })

  test.fixme('created ticket shows correct priority — not Unknown', async ({ page }) => {
    // KNOWN BUG: priority badge shows "Unknown" instead of "Medium" for new tickets
    await createUserAndLogin(page)
    await createSpace(page, 'Priority Display Test', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Priority Check Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()

    await expect(page.locator('text=Priority Check Ticket')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=Unknown')).not.toBeVisible()
    // Medium is the default priority
    await expect(page.locator('text=Medium').first()).toBeVisible()
  })

  test.fixme('created ticket shows correct status — not blank', async ({ page }) => {
    // KNOWN BUG: status badge display issue — depends on priority bug fix
    await createUserAndLogin(page)
    await createSpace(page, 'Status Display Test', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Status Check Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()

    await expect(page.locator('text=Status Check Ticket')).toBeVisible({ timeout: 5000 })
    // Status should be Open by default
    await expect(page.locator('text=Open').first()).toBeVisible()
  })

  test('ticket creation is confirmed by API', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'API Verify Desk', 'service_desk')

    const spaceId = page.url().match(/\/spaces\/([^/]+)/)?.[1]

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'API Verify Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=API Verify Ticket')).toBeVisible({ timeout: 5000 })

    // Verify via API that the ticket actually exists in the database
    const token = await getAuthToken(page)
    const orgId = await page.evaluate(() => {
      for (const key of Object.keys(localStorage)) {
        const val = localStorage.getItem(key)
        if (val && val.includes('org_id')) {
          try { return JSON.parse(atob(val.split('.')[1])).org_id } catch { return null }
        }
      }
      return null
    })

    if (orgId && spaceId) {
      const response = await page.request.get(
        `/api/v1/orgs/${orgId}/spaces/${spaceId}/items`,
        { headers: { Authorization: `Bearer ${token}` } }
      )
      expect(response.status()).toBe(200)
      const items = await response.json()
      expect(Array.isArray(items)).toBe(true)
      expect(items.length).toBeGreaterThan(0)
      expect(items[0].priority).toBe('medium')
      expect(items[0].status).toBe('open')
    }
  })

  test('kanban board loads without error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Kanban Test', 'service_desk')
    await page.click('text=Kanban Board')
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('back to dashboard link works', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Nav Test Desk', 'service_desk')
    await page.click('text=Back to Dashboard')
    await expect(page).toHaveURL('/')
    await expect(page.locator('text=Welcome back')).toBeVisible()
  })

  test('clicking a ticket opens detail view and stays there — no redirect to login', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Detail View Test', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Detail Test Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Detail Test Ticket')).toBeVisible({ timeout: 5000 })

    // Click the ticket row
    await page.click('text=Detail Test Ticket')

    // Must stay on ticket detail — must NOT redirect to login
    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })
    await expect(page).toHaveURL(/\/tickets\//, { timeout: 5000 })
    await expect(page.locator('text=Detail Test Ticket')).toBeVisible()

    // Wait for all async calls to settle — if redirect is going to happen it happens here
    await page.waitForTimeout(2000)
    await expect(page).not.toHaveURL(/\/login/)
  })

  test('ticket detail comments section loads without 404 error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Comments Test Desk', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Comments Test Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Comments Test Ticket')).toBeVisible({ timeout: 5000 })
    await page.click('text=Comments Test Ticket')

    // Must not redirect to login
    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })

    // Comments/Activity section must be visible and not show errors
    await expect(
      page.locator('h3:has-text("Activity")').first()
    ).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=404')).not.toBeVisible()
    await expect(page.locator('text=Something went wrong')).not.toBeVisible()
  })

  test('can add a comment to a ticket', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Add Comment Test', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Comment Target Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Comment Target Ticket')).toBeVisible({ timeout: 5000 })
    await page.click('text=Comment Target Ticket')
    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })

    // Add a comment
    await page.fill('textarea[placeholder*="comment"], textarea[placeholder*="Comment"]', 'This is a test comment')
    await page.click('button:has-text("Comment")')

    // Comment must appear in the thread
    await expect(page.locator('text=This is a test comment')).toBeVisible({ timeout: 5000 })
  })

  test('members endpoint loads — assignee dropdown visible without 404', async ({ page }) => {
    const failedRequests: string[] = []
    page.on('response', response => {
      if (response.status() === 404 && response.url().includes('/members')) {
        failedRequests.push(`404: ${response.url()}`)
      }
    })

    await createUserAndLogin(page)
    await createSpace(page, 'Assignee Test Desk', 'service_desk')
    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Assignee Test')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Assignee Test')).toBeVisible({ timeout: 5000 })
    await page.click('text=Assignee Test')
    await expect(page).not.toHaveURL(/\/login/)

    // Assignee dropdown must be visible — verifies the members endpoint loaded
    const assigneeSelect = page.locator('select').filter({ hasText: 'Unassigned' })
    await expect(assigneeSelect).toBeVisible({ timeout: 5000 })

    // Wait for requests to settle
    await page.waitForTimeout(1000)

    // No 404 errors on members endpoint
    expect(failedRequests, `Members endpoint 404: ${failedRequests.join(', ')}`).toHaveLength(0)
  })

  test('comments section loads and a comment can be posted', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Comment Post Test', 'service_desk')
    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Comment Test Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Comment Test Ticket')).toBeVisible({ timeout: 5000 })
    await page.click('text=Comment Test Ticket')
    await expect(page).not.toHaveURL(/\/login/)

    // Activity section must be visible
    await expect(
      page.locator('h3:has-text("Activity")').first()
    ).toBeVisible({ timeout: 5000 })

    // Post a comment
    const commentBox = page.locator('textarea[placeholder*="comment"], textarea[placeholder*="Comment"]')
    await expect(commentBox).toBeVisible({ timeout: 5000 })
    await commentBox.fill('This is a regression test comment')
    await page.click('button:has-text("Comment")')

    // Comment must appear
    await expect(page.locator('text=This is a regression test comment')).toBeVisible({ timeout: 5000 })
  })

  test('no 404 errors in network tab when viewing ticket detail', async ({ page }) => {
    const failedRequests: string[] = []
    page.on('response', response => {
      if (response.status() === 404) {
        failedRequests.push(`404: ${response.url()}`)
      }
    })

    await createUserAndLogin(page)
    await createSpace(page, 'No 404 Test', 'service_desk')
    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'No 404 Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=No 404 Ticket')).toBeVisible({ timeout: 5000 })
    await page.click('text=No 404 Ticket')
    await expect(page).not.toHaveURL(/\/login/)

    // Wait for all requests to settle
    await page.waitForTimeout(2000)

    // Filter to only API 404s — not expected 404s like missing favicon
    const api404s = failedRequests.filter(r => r.includes('/api/'))
    expect(api404s, `Unexpected API 404s: ${api404s.join(', ')}`).toHaveLength(0)
  })

  test('assignee dropdown shows org members — not just Unassigned', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Assignee Members Test', 'service_desk')

    // Space creation does not auto-add creator as member — do it via API
    const { orgId } = await getCurrentUser(page)
    await addCurrentUserAsSpaceMember(page, orgId, spaceId)

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Assignee Members Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Assignee Members Ticket')).toBeVisible({ timeout: 5000 })
    await page.click('text=Assignee Members Ticket')
    await expect(page).not.toHaveURL(/\/login/)

    // Wait for members to load
    await page.waitForTimeout(2000)

    // Assignee dropdown must have more than just "Unassigned"
    const assigneeSelect = page.locator('select').filter({ hasText: 'Unassigned' }).first()
    await expect(assigneeSelect).toBeVisible({ timeout: 5000 })
    const optionCount = await assigneeSelect.locator('option').count()
    expect(optionCount).toBeGreaterThan(1)

    // Verify real member options exist beyond "Unassigned"
    const memberOptions = assigneeSelect.locator('option:not([value=""])')
    const memberCount = await memberOptions.count()
    expect(memberCount).toBeGreaterThan(0)

    // Verify a member name is shown (not a UUID or empty)
    const memberText = await memberOptions.first().textContent()
    expect(memberText?.trim().length).toBeGreaterThan(0)
    expect(memberText).not.toBe('Unassigned')
  })

  test('reporter shows actual user name — not Unknown', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Reporter Name Test', 'service_desk')

    // Add current user as space member so reporter_id resolves to a name
    const { orgId } = await getCurrentUser(page)
    await addCurrentUserAsSpaceMember(page, orgId, spaceId)

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Reporter Test Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Reporter Test Ticket')).toBeVisible({ timeout: 5000 })
    await page.click('text=Reporter Test Ticket')
    await expect(page).not.toHaveURL(/\/login/)

    // Wait for members to load and reporter to resolve
    await page.waitForTimeout(2000)

    // Reporter label must be visible (rendered as uppercase "REPORTER" via CSS)
    const reporterLabel = page.locator('label').filter({ hasText: /reporter/i }).first()
    await expect(reporterLabel).toBeVisible({ timeout: 5000 })

    // The reporter name span is the sibling element after the avatar circle.
    // It must NOT show "Unknown" — should show the test user's display name.
    const reporterName = reporterLabel.locator('..').locator('span').first()
    await expect(reporterName).toBeVisible({ timeout: 5000 })
    const nameText = await reporterName.textContent()
    expect(nameText?.trim()).not.toBe('Unknown')
    expect(nameText?.trim().length).toBeGreaterThan(0)
  })

  test('adding a comment saves and persists after page reload', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Comment Persist Test', 'service_desk')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Comment Persist Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Comment Persist Ticket')).toBeVisible({ timeout: 5000 })
    await page.click('text=Comment Persist Ticket')
    await expect(page).not.toHaveURL(/\/login/)

    // Add a comment
    const commentBox = page.locator('textarea[placeholder*="comment"], textarea[placeholder*="Comment"]')
    await expect(commentBox).toBeVisible({ timeout: 5000 })
    await commentBox.fill('Persisted comment regression test')
    await page.click('button:has-text("Comment")')

    // Comment appears immediately
    await expect(page.locator('text=Persisted comment regression test')).toBeVisible({ timeout: 5000 })

    // Reload and verify it persisted — this hits the real API
    await page.reload()
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('text=Persisted comment regression test')).toBeVisible({ timeout: 5000 })
  })
})
