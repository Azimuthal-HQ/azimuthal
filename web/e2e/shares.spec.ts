import { test, expect } from '@playwright/test'
import {
  createUserAndLogin,
  createSpace,
  getAuthToken,
  getCurrentUser,
  assertNoErrors,
} from './helpers/setup'

// Entity shares (P3, ADR-0008). The org owner (an org admin) shares a page
// org-wide, then reads it through the standalone shared route — which renders
// outside the app shell — and confirms the persistent ShareBadge. Every step
// asserts no raw backend string leaked (friendlyErrorMessage discipline).

// createPage creates a wiki page via the API and returns its id.
async function createPage(page: import('@playwright/test').Page, orgId: string, spaceId: string, title: string): Promise<string> {
  const token = await getAuthToken(page)
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/wiki`, {
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    data: { title, content: '# ' + title + '\n\nShared body text.' },
  })
  if (res.status() !== 201) throw new Error(`createPage failed: ${res.status()} ${await res.text()}`)
  const body = await res.json() as { id: string }
  return body.id
}

test.describe('Entity shares', () => {
  test('admin shares a page org-wide and reads it via the standalone route', async ({ page }) => {
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)
    const spaceId = await createSpace(page, 'Shares Codex', 'codex')

    const run = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    const title = `Shared Page ${run}`
    const pageId = await createPage(page, orgId, spaceId, title)

    // Open the wiki page and its Share dialog.
    await page.goto(`/codex/${spaceId}/pages/${pageId}`)
    await expect(page.getByTestId('wiki-share-button')).toBeVisible({ timeout: 15000 })
    await page.getByTestId('wiki-share-button').click()
    await expect(page.getByTestId('share-dialog')).toBeVisible()

    // Org audience is the default; share it.
    await expect(page.getByTestId('share-empty')).toBeVisible()
    await page.getByTestId('share-submit').click()
    await expect(page.getByTestId('share-row')).toHaveCount(1)
    await assertNoErrors(page)
    await page.getByTestId('share-close').click()

    // The page now carries the persistent ShareBadge.
    await expect(page.getByTestId('share-badge').first()).toBeVisible()

    // Read it through the standalone shared route — outside the app shell.
    await page.goto(`/shared/page/${pageId}`)
    await expect(page.getByTestId('shared-view')).toBeVisible({ timeout: 15000 })
    await expect(page.getByTestId('shared-entity')).toBeVisible()
    await expect(page.getByTestId('shared-breadcrumb')).toHaveText(title)
    await expect(page.getByTestId('share-badge').first()).toBeVisible()
    // No space chrome: the Codex sidebar tree must not be present here.
    await expect(page.getByTestId('codex-page-tree')).toHaveCount(0)
    await assertNoErrors(page)
  })

  test('revoking a share removes standalone access', async ({ page }) => {
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)
    const spaceId = await createSpace(page, 'Revoke Codex', 'codex')
    const token = await getAuthToken(page)

    const run = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    const pageId = await createPage(page, orgId, spaceId, `Revocable ${run}`)

    // Share via API, confirm the standalone route reads, then revoke.
    const created = await page.request.post(`/api/v1/orgs/${orgId}/shares`, {
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      data: { entity_type: 'page', entity_id: pageId, audience: 'org' },
    })
    expect(created.status()).toBe(201)
    const share = await created.json() as { id: string }

    await page.goto(`/shared/page/${pageId}`)
    await expect(page.getByTestId('shared-entity')).toBeVisible({ timeout: 15000 })

    const revoked = await page.request.delete(`/api/v1/orgs/${orgId}/shares/${share.id}`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(revoked.status()).toBe(204)

    // The very next load of the standalone route is denied — friendly, not raw.
    await page.goto(`/shared/page/${pageId}`)
    await expect(page.getByTestId('shared-not-available')).toBeVisible({ timeout: 15000 })
    await assertNoErrors(page)
  })
})
