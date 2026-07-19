import { test, expect } from '@playwright/test'
import { createUserAndLogin, assertNoErrors } from './helpers/setup'

// Regression (spec §2.7): WorkflowAdminPage shipped with a private fetch
// client reading the localStorage key 'azimuthal_token', which nothing ever
// writes — the real client stores the token under 'azimuthal_access_token'.
// Every request from this page therefore carried an empty bearer token, the
// API answered 401, and the page rendered "Failed to load workflows." for
// every user, always. Verified against a live instance before the fix:
// GET /orgs/{org}/workflows with `Authorization: Bearer ` → 401; with the
// real token → 200. The page now routes through lib/api.ts. This test pins
// the authenticated round trip at the network level and the rendered result,
// so it fails against the pre-fix page and passes after.
test('workflow admin page loads org workflows through the shared api client', async ({ page }) => {
  await createUserAndLogin(page)

  const listResponse = page.waitForResponse(
    (r) => /\/api\/v1\/orgs\/[0-9a-f-]+\/workflows$/.test(r.url()) && r.request().method() === 'GET',
  )
  const statesResponse = page.waitForResponse(
    (r) =>
      /\/api\/v1\/orgs\/[0-9a-f-]+\/workflows\/[0-9a-f-]+\/states$/.test(r.url()) &&
      r.request().method() === 'GET',
  )

  await page.goto('/admin/workflows')

  expect((await listResponse).status()).toBe(200)
  expect((await statesResponse).status()).toBe(200)

  // A fresh org is seeded with exactly these two default workflows.
  await expect(page.getByText('Default Service Desk', { exact: true })).toBeVisible()
  await expect(page.getByText('Default Project', { exact: true })).toBeVisible()

  // A state unique to each workflow renders inside its card — 'resolved'
  // exists only in the ticket workflow, 'in_review' only in the project one.
  // ('in_progress' and 'done' are unusable here: they appear as both state
  // names and category badges.)
  await expect(page.getByText('resolved', { exact: true })).toBeVisible()
  await expect(page.getByText('in_review', { exact: true })).toBeVisible()

  await assertNoErrors(page)
})
