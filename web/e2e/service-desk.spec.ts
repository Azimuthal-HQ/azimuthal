import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors, getAuthToken, getCurrentUser, addCurrentUserAsSpaceMember } from './helpers/setup'

test.describe('Service Desk', () => {
  test('can create a service desk space and land on ticket list', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'E2E Service Desk', 'beacon')
    await expect(page).toHaveURL(/\/beacon\/.*\/tickets/, { timeout: 10000 })
    await expect(page.locator('h1:has-text("Tickets"), h2:has-text("Tickets"), [role="heading"]:has-text("Tickets")').first()).toBeVisible()
    await assertNoErrors(page)
  })

  test('ticket list loads without error on empty space', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Empty Desk', 'beacon')
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('Reports view renders content — regression: blank screen', async ({ page }) => {
    // P0 defect: the sidebar linked to /spaces/:id/reports but no route existed,
    // so React Router rendered nothing — an entirely blank document body.
    await createUserAndLogin(page)
    await createSpace(page, 'Reports Desk', 'beacon')

    await page.getByRole('link', { name: 'Reports' }).click()
    await expect(page).toHaveURL(/\/beacon\/.*\/reports/, { timeout: 10000 })

    // The route must render non-empty content — a blank body is never acceptable.
    await expect(page.locator('h1:has-text("Reports")')).toBeVisible({ timeout: 5000 })
    expect((await page.locator('body').innerText()).trim()).not.toBe('')
    await assertNoErrors(page)
  })

  test('can create a ticket with minimum fields', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Ticket Create Test', 'beacon')

    await page.click('button:has-text("New Ticket")')
    await expect(page.locator('#ticket-title')).toBeVisible()

    await page.fill('#ticket-title', 'E2E Test Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()

    await expect(page.locator('text=E2E Test Ticket')).toBeVisible({ timeout: 5000 })
  })

  test('created ticket shows correct priority — not Unknown', async ({ page }) => {
    // Audit ref: testing-audit.md §3.3.
    // PRIORITY_LABEL maps "medium" → "Medium" with a "Medium" fallback (no
    // "Unknown" string is rendered for unmapped values). The original
    // `text=Medium`.first() locator picked the hidden filter `<option>` in
    // the toolbar; scope to the row to assert the badge text exactly.
    await createUserAndLogin(page)
    await createSpace(page, 'Priority Display Test', 'beacon')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Priority Check Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()

    await expect(page.locator('text=Priority Check Ticket')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=Unknown')).not.toBeVisible()
    // Priority cell of the row for the new ticket. Medium is the default
    // priority so the badge text must equal exactly "Medium".
    const priorityCell = page
      .locator('table tbody tr')
      .filter({ hasText: 'Priority Check Ticket' })
      .locator('td')
      .nth(3)
    await expect(priorityCell).toHaveText('Medium')
  })

  test('created ticket shows correct status — not blank', async ({ page }) => {
    // Audit ref: testing-audit.md §3.3.
    // STATUS_LABEL["open"] resolves to "Open" — the default for new tickets.
    // Scope to the row's status cell so the assertion does not match the
    // hidden filter `<option value="open">Open</option>` in the toolbar.
    await createUserAndLogin(page)
    await createSpace(page, 'Status Display Test', 'beacon')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Status Check Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()

    await expect(page.locator('text=Status Check Ticket')).toBeVisible({ timeout: 5000 })
    const statusCell = page
      .locator('table tbody tr')
      .filter({ hasText: 'Status Check Ticket' })
      .locator('td')
      .nth(2)
    await expect(statusCell).toHaveText('Open')
  })

  test('ticket creation is confirmed by API', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'API Verify Desk', 'beacon')

    const spaceId = page.url().match(/\/beacon\/([^/]+)/)?.[1]

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

  test('kanban card dragged to another column persists across reload — regression: drag not wired', async ({ page }) => {
    // P0 defect: the board rendered dnd-kit scaffolding but the drag-end
    // handler did nothing — no status change, no persistence. Note the
    // pre-fix board also had no per-column landmarks, so on the unfixed UI
    // this fails at the first column locator.
    await createUserAndLogin(page)
    await createSpace(page, 'Drag Desk', 'beacon')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Drag Me Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Drag Me Ticket')).toBeVisible({ timeout: 5000 })

    await page.getByTestId('space-sidebar').getByRole('link', { name: 'Board', exact: true }).click()
    await expect(page).toHaveURL(/\/beacon\/.*\/board/, { timeout: 10000 })

    const card = page.locator('[data-column-id="open"]').locator('text=Drag Me Ticket')
    await expect(card).toBeVisible({ timeout: 5000 })

    // dnd-kit's PointerSensor needs a real pointer gesture: press, move past
    // the 5px activation threshold in small steps, glide to the target column.
    const cardBox = await card.boundingBox()
    const target = page.locator('[data-column-id="in_progress"]')
    const targetBox = await target.boundingBox()
    if (!cardBox || !targetBox) throw new Error('could not measure drag source/target')

    await page.mouse.move(cardBox.x + cardBox.width / 2, cardBox.y + cardBox.height / 2)
    await page.mouse.down()
    await page.mouse.move(cardBox.x + cardBox.width / 2 + 12, cardBox.y + cardBox.height / 2, { steps: 4 })
    await page.mouse.move(targetBox.x + targetBox.width / 2, targetBox.y + Math.min(120, targetBox.height / 2), { steps: 15 })
    await page.mouse.up()

    // The card lands in In Progress…
    await expect(page.locator('[data-column-id="in_progress"]').locator('text=Drag Me Ticket')).toBeVisible({ timeout: 5000 })

    // …and the move survives a reload — persisted through the API, not just local state.
    await page.reload()
    await expect(page.locator('[data-column-id="in_progress"]').locator('text=Drag Me Ticket')).toBeVisible({ timeout: 10000 })
    await expect(page.locator('[data-column-id="open"]').locator('text=Drag Me Ticket')).not.toBeVisible()
  })

  test('kanban board loads without error', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Kanban Test', 'beacon')
    await page.getByTestId('space-sidebar').getByRole('link', { name: 'Board', exact: true }).click()
    await assertNoErrors(page)
    await expect(page.locator('text=Unknown')).not.toBeVisible()
  })

  test('Home product tab returns to the overview from a space', async ({ page }) => {
    // The old sidebar's "Back to Dashboard" link is gone (ADR-0005): the way
    // home is the Home tab in the top-bar product switcher.
    await createUserAndLogin(page)
    await createSpace(page, 'Nav Test Desk', 'beacon')
    await page.getByTestId('product-tab-home').click()
    await expect(page).toHaveURL('/')
    await expect(page.locator('text=Welcome back')).toBeVisible()
  })

  test('clicking a ticket opens detail view and stays there — no redirect to login', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Detail View Test', 'beacon')

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
    await createSpace(page, 'Comments Test Desk', 'beacon')

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
    await createSpace(page, 'Add Comment Test', 'beacon')

    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Comment Target Ticket')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Comment Target Ticket')).toBeVisible({ timeout: 5000 })
    await page.click('text=Comment Target Ticket')
    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })

    // Add a comment
    await page.fill('textarea[placeholder*="comment"], textarea[placeholder*="Comment"]', 'This is a test comment')
    await page.getByRole('button', { name: 'Comment', exact: true }).click()

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
    await createSpace(page, 'Assignee Test Desk', 'beacon')
    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Assignee Test')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    // Exact link-role locator: the space picker's accessible name ("Assignee
    // Test Desk …") contains this ticket title, so a bare text= match hits
    // the sidebar first.
    const ticketLink = page.getByRole('link', { name: 'Assignee Test', exact: true })
    await expect(ticketLink).toBeVisible({ timeout: 5000 })
    await ticketLink.click()
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
    await createSpace(page, 'Comment Post Test', 'beacon')
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
    await page.getByRole('button', { name: 'Comment', exact: true }).click()

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
    await createSpace(page, 'No 404 Test', 'beacon')
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
    const spaceId = await createSpace(page, 'Assignee Members Test', 'beacon')

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
    const spaceId = await createSpace(page, 'Reporter Name Test', 'beacon')

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
    await createSpace(page, 'Comment Persist Test', 'beacon')

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
    await page.getByRole('button', { name: 'Comment', exact: true }).click()

    // Comment appears immediately
    await expect(page.locator('text=Persisted comment regression test')).toBeVisible({ timeout: 5000 })

    // Reload and verify it persisted — this hits the real API
    await page.reload()
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('text=Persisted comment regression test')).toBeVisible({ timeout: 5000 })
  })
})
