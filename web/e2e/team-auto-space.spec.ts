import { test, expect } from '@playwright/test'
import { createUserAndLogin, getAuthToken, getCurrentUser } from './helpers/setup'

// S10(a): creating a team with a module's space checkbox ticked creates a
// space of that module named for the team, with the team granted access via
// the existing space_grants path.
test.describe('Team auto-space — S10', () => {
  test('creating a team with Codex checked creates a Codex space the team can access', async ({ page }) => {
    await createUserAndLogin(page) // an org admin
    const teamName = `AutoSpace ${Date.now()}`

    await page.goto('/admin/teams')
    await page.getByTestId('team-create-button').click()
    await page.getByTestId('team-create-name').fill(teamName)
    await page.getByTestId('team-create-space-codex').check()
    await page.getByTestId('team-create-space-beacon').uncheck() // ensure default-unchecked stays unchecked
    await page.getByTestId('team-create-submit').click()

    // The team row appears once the orchestration completes.
    await expect(page.getByText(teamName).first()).toBeVisible({ timeout: 15000 })

    // Verify the Codex space exists and the team holds a grant on it.
    const token = await getAuthToken(page)
    const { orgId } = await getCurrentUser(page)

    const spacesRes = await page.request.get(`/api/v1/orgs/${orgId}/spaces`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(spacesRes.status()).toBe(200)
    const spaces = (await spacesRes.json()) as Array<{ id: string; name: string; type: string }>
    const codexSpace = spaces.find((s) => s.name === teamName && s.type === 'codex')
    expect(codexSpace, 'a codex space named for the team must exist').toBeTruthy()

    // No beacon/vector space (those boxes were unchecked).
    expect(spaces.some((s) => s.name === teamName && s.type !== 'codex')).toBe(false)

    const grantsRes = await page.request.get(
      `/api/v1/orgs/${orgId}/spaces/${codexSpace!.id}/grants`,
      { headers: { Authorization: `Bearer ${token}` } },
    )
    expect(grantsRes.status()).toBe(200)
    const grants = (await grantsRes.json()) as Array<{ subject_type: string }>
    expect(grants.some((g) => g.subject_type === 'team'), 'the team must hold a grant on its space').toBe(true)
  })
})
