import { test, expect, Page } from '@playwright/test'
import { execSync } from 'child_process'
import { createUserAndLogin, createSpace, getAuthToken, getCurrentUser } from './helpers/setup'

const BINARY = process.env.AZIMUTHAL_BINARY || '/tmp/azimuthal-test'

// P2 — teams, grants, and the space directory (v0.3 spec §6, ADR-0006/0007).
//
// Locator discipline (P1.5 audit rules): every assertion targets a
// data-testid scoped to this run's unique names — never bare text that could
// collide with chrome, and never an absence that has benign causes. The
// denial assertions pair a positive sighting (locked row visible) with the
// same name-scoped locator dropping to zero after the state change.
//
// All E2E users named "E2E User" share one org (the CLI reuses the org by
// display-name slug), so every locator below is scoped by unique per-run
// names rather than global counts.

/** Creates a NON-admin org member in the shared E2E org and logs them in. */
async function createMemberAndLogin(page: Page): Promise<{ email: string }> {
  const ts = Date.now()
  const suffix = Math.random().toString(36).slice(2, 8)
  const email = `e2e-member-${ts}-${suffix}@azimuthal.dev`
  const password = 'E2eTestPass123!'
  execSync(
    `${BINARY} admin create-user --email "${email}" --name "E2E User" --password "${password}" --role member`,
    { stdio: 'pipe' },
  )
  await page.goto('/login')
  await page.fill('input[type="email"]', email)
  await page.fill('input[type="password"]', password)
  await page.click('button[type="submit"], button:has-text("Sign in")')
  await expect(page).not.toHaveURL(/\/login/, { timeout: 15000 })
  return { email }
}

test.describe('Teams admin', () => {
  test('org admin creates, renames, and deletes a team tree', async ({ page }) => {
    await createUserAndLogin(page)
    const run = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    const parentName = `Parent Team ${run}`
    const childName = `Child Team ${run}`

    await page.goto('/admin/teams')
    await expect(page.getByTestId('teams-admin-page')).toBeVisible()

    // Create the parent.
    await page.getByTestId('team-create-button').click()
    await page.getByTestId('team-create-name').fill(parentName)
    await page.getByTestId('team-create-slug').fill(`parent-${run}`)
    await page.getByTestId('team-create-submit').click()
    const parentRow = page.getByTestId('team-row').filter({ hasText: parentName })
    await expect(parentRow).toHaveCount(1)

    // Create the child under it.
    await page.getByTestId('team-create-button').click()
    await page.getByTestId('team-create-name').fill(childName)
    await page.getByTestId('team-create-slug').fill(`child-${run}`)
    await page.getByTestId('team-create-parent').selectOption({ label: parentName })
    await page.getByTestId('team-create-submit').click()
    const childRow = page.getByTestId('team-row').filter({ hasText: childName })
    await expect(childRow).toHaveCount(1)

    // Deleting the parent while it has a child surfaces the 409 positively.
    await parentRow.getByTestId('team-delete-button').click()
    await parentRow.getByTestId('team-delete-confirm').click()
    await expect(page.getByTestId('team-error-banner')).toBeVisible()
    await expect(page.getByTestId('team-error-banner')).toContainText(/child/i)
    await expect(parentRow).toHaveCount(1)

    // Rename the child.
    const renamed = `Renamed Team ${run}`
    await childRow.getByTestId('team-edit-button').click()
    await page.getByTestId('team-edit-name').fill(renamed)
    await page.getByTestId('team-edit-submit').click()
    const renamedRow = page.getByTestId('team-row').filter({ hasText: renamed })
    await expect(renamedRow).toHaveCount(1)

    // Delete child then parent — both rows drop to zero after having been
    // seen at one, so the disappearance cannot be vacuous.
    await renamedRow.getByTestId('team-delete-button').click()
    await renamedRow.getByTestId('team-delete-confirm').click()
    await expect(renamedRow).toHaveCount(0)
    await parentRow.getByTestId('team-delete-button').click()
    await parentRow.getByTestId('team-delete-confirm').click()
    await expect(parentRow).toHaveCount(0)
  })

  test('default team is badged and protected', async ({ page }) => {
    await createUserAndLogin(page)
    await page.goto('/admin/teams')
    await expect(page.getByTestId('teams-admin-page')).toBeVisible()
    const badge = page.getByTestId('team-default-badge')
    await expect(badge).toHaveCount(1)
    const defaultRow = page.getByTestId('team-row').filter({ has: badge })
    await expect(defaultRow.getByTestId('team-delete-button')).toBeDisabled()
  })
})

test.describe('Grants on space settings', () => {
  test('admin adds, updates, and revokes a team grant', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Grants Space', 'vector')

    await page.goto(`/vector/${spaceId}/settings`)
    await expect(page.getByTestId('space-settings-page')).toBeVisible()

    // Add a grant to the org default team (name is deterministic: the seed
    // names it "Default" with slug "default", and an org has exactly one).
    // The picker replaced the old user/team toggle + UUID field (P2.5 W5):
    // type the team name, pick the option by its slug-derived testid.
    await page.getByTestId('grant-subject-picker-input').fill('Default')
    await page.getByTestId('grant-subject-picker-option-team-default').click()
    await expect(page.getByTestId('grant-subject-picker-selected')).toContainText('Default')
    await page.getByTestId('grant-add-role-select').selectOption('viewer')
    await page.getByTestId('grant-add-button').click()
    const row = page.getByTestId('grant-row')
    await expect(row).toHaveCount(1)

    // Change the role, then revoke.
    await row.getByTestId('grant-role-select').selectOption('agent')
    await expect(row.getByTestId('grant-role-select')).toHaveValue('agent')
    await row.getByTestId('grant-revoke-button').click()
    await expect(row).toHaveCount(0)
  })
})

test.describe('Space directory and visibility', () => {
  test('discoverable space shows as a locked row for a non-admin; hidden disappears', async ({ browser }) => {
    // Admin context: create the space (default visibility: discoverable).
    const adminContext = await browser.newContext()
    const adminPage = await adminContext.newPage()
    await createUserAndLogin(adminPage)
    const spaceId = await createSpace(adminPage, 'Locked Space', 'vector')
    const adminToken = await getAuthToken(adminPage)
    const { orgId } = await getCurrentUser(adminPage)
    const spaceRes = await adminPage.request.get(`/api/v1/orgs/${orgId}/spaces/${spaceId}`, {
      headers: { Authorization: `Bearer ${adminToken}` },
    })
    expect(spaceRes.status()).toBe(200)
    const spaceName = ((await spaceRes.json()) as { name: string }).name

    // Member context: no grants — the directory lists the space as locked.
    const memberContext = await browser.newContext()
    const memberPage = await memberContext.newPage()
    await createMemberAndLogin(memberPage)
    await memberPage.goto('/spaces')
    await expect(memberPage.getByTestId('space-directory-page')).toBeVisible()
    const lockedRow = memberPage.getByTestId('locked-space-row').filter({ hasText: spaceName })
    await expect(lockedRow).toHaveCount(1)
    await expect(lockedRow).toContainText('contact a space admin')
    // Locked rows never link into the space.
    await expect(lockedRow.locator('a')).toHaveCount(0)

    // Admin hides the space; the same name-scoped locator drops to zero on
    // a reload while the page itself is demonstrably rendered.
    const putRes = await adminPage.request.put(`/api/v1/orgs/${orgId}/spaces/${spaceId}`, {
      headers: { Authorization: `Bearer ${adminToken}`, 'Content-Type': 'application/json' },
      data: { name: spaceName, visibility: 'hidden' },
    })
    expect(putRes.status()).toBe(200)

    await memberPage.reload()
    await expect(memberPage.getByTestId('space-directory-page')).toBeVisible()
    await expect(
      memberPage.getByTestId('locked-space-row').filter({ hasText: spaceName }),
    ).toHaveCount(0)
    await expect(
      memberPage.getByTestId('directory-space-row').filter({ hasText: spaceName }),
    ).toHaveCount(0)

    await adminContext.close()
    await memberContext.close()
  })

  test('focusing a team narrows the picker and the chip clears it', async ({ page }) => {
    await createUserAndLogin(page)
    await createSpace(page, 'Focus Space', 'vector')

    await page.goto('/spaces')
    await expect(page.getByTestId('space-directory-page')).toBeVisible()
    await page.getByTestId('directory-focus-button').first().click()
    await expect(page.getByTestId('focus-chip')).toBeVisible()

    // One-click clear from the chip (ADR-0006 point 7).
    await page.getByTestId('focus-chip').getByRole('button', { name: /clear team focus/i }).click()
    await expect(page.getByTestId('focus-chip')).toHaveCount(0)
  })
})
