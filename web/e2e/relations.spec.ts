import { test, expect } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors } from './helpers/setup'

/**
 * A4: entity-generic relations, the round trip the read path used to lose.
 *
 * A relation to a page could be CREATED for as long as the write path has
 * been readable-target-checked — and then never displayed, because the read
 * query had no pages arm. The proof the capability now round-trips is a real
 * browser linking a real item to a real page through the picker, seeing the
 * page's title render in the relations panel, and following the link to the
 * page itself. Every hop of that used to be impossible for a different
 * reason: no page suggest to type into, no to_type on the wire, no far arm to
 * resolve the title, no far_space_id to build the href from.
 */

test.describe('Relations', () => {
  test('an item links to a page through the picker and the link reaches the page', async ({ page }) => {
    await createUserAndLogin(page)

    // A page to link to, in a codex space. The title is unique per run so the
    // org-wide suggest cannot match a leftover from another worker.
    const pageTitle = `Linked Runbook ${Date.now()}`
    await createSpace(page, 'Relations Wiki', 'codex')
    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', pageTitle)
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.getByTestId('wiki-page-title')).toContainText(pageTitle, { timeout: 5000 })

    // An item to link from, in a vector space.
    await createSpace(page, 'Relations Project', 'vector')
    await page.click('button:has-text("Create Item")')
    await page.fill('#item-title', 'Item That Cites The Runbook')
    await page.locator('[role="dialog"] button:has-text("Create Item")').click()
    await expect(page.locator('text=Item That Cites The Runbook')).toBeVisible({ timeout: 5000 })

    await page.click('text=Item That Cites The Runbook')
    await expect(page).toHaveURL(/\/backlog\//, { timeout: 5000 })
    await expect(page.getByTestId('relations-section')).toBeVisible()

    // Link it to the page: kind, target type, then the typeahead.
    await page.getByLabel('Relation kind').selectOption('wiki_link')
    await page.getByLabel('Relation target type').selectOption('page')

    const created = page.waitForResponse(
      (r) => r.request().method() === 'POST' && /\/relations$/.test(r.url()),
    )
    await page.getByTestId('relation-page-ref').fill(pageTitle)
    await page.getByTestId('relation-page-ref-suggestions').getByText(pageTitle).click()
    expect((await created).status(), 'the relation POST must succeed').toBe(201)

    // The far side renders: the page's TITLE, as a link. This is the half a
    // user sees, and the half that was missing.
    const farLink = page.getByTestId('relations-section').getByRole('link', { name: pageTitle })
    await expect(farLink).toBeVisible({ timeout: 5000 })

    // Follow it: the link must land on the page itself, in the page's OWN
    // space — the href is built from far_space_id, not this item's space.
    await farLink.click()
    await expect(page).toHaveURL(/\/codex\/.*\/pages\//, { timeout: 10000 })
    await expect(page.getByTestId('wiki-page-title')).toContainText(pageTitle, { timeout: 5000 })
    await assertNoErrors(page)
  })

  test('the relations surface renders on tickets and links a page from the ticket side', async ({ page }) => {
    await createUserAndLogin(page)

    const pageTitle = `Ticket Runbook ${Date.now()}`
    await createSpace(page, 'Ticket Relations Wiki', 'codex')
    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', pageTitle)
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.getByTestId('wiki-page-title')).toContainText(pageTitle, { timeout: 5000 })

    await createSpace(page, 'Relations Desk', 'beacon')
    await page.click('button:has-text("New Ticket")')
    await page.fill('#ticket-title', 'Ticket That Cites The Runbook')
    await page.locator('[role="dialog"] button:has-text("Create Ticket")').click()
    await expect(page.locator('text=Ticket That Cites The Runbook')).toBeVisible({ timeout: 5000 })

    await page.click('text=Ticket That Cites The Runbook')
    await expect(page).toHaveURL(/\/tickets\//, { timeout: 5000 })

    // The reciprocal that makes the track feel finished: tickets carry the
    // same relations surface project items do.
    await expect(page.getByTestId('relations-section')).toBeVisible()

    const created = page.waitForResponse(
      (r) => r.request().method() === 'POST' && /\/relations$/.test(r.url()),
    )
    await page.getByTestId('relation-page-ref').fill(pageTitle)
    await page.getByTestId('relation-page-ref-suggestions').getByText(pageTitle).click()
    expect((await created).status(), 'the ticket-side relation POST must succeed').toBe(201)

    await expect(
      page.getByTestId('relations-section').getByRole('link', { name: pageTitle }),
    ).toBeVisible({ timeout: 5000 })
    await assertNoErrors(page)
  })

  /**
   * C4: the journey the maintainer actually attempted. A page in one space is
   * linked to a page in ANOTHER space through the panel on the page detail
   * view — the control that never existed until now, even though the write
   * path has accepted the link since A4. Both directions must render (the
   * incoming arm the read query unions), and following either far link must
   * land on the other page in ITS OWN space's chrome, since the href is built
   * from far_space_id rather than the near page's space.
   */
  test('a page links to a page in another space through the panel, and both far links cross spaces', async ({ page }) => {
    await createUserAndLogin(page)

    // Titles carry the run stamp so the org-wide page suggest cannot match a
    // leftover from another worker, and so each far link's accessible name is
    // unique to this test.
    const stamp = Date.now()
    const pageATitle = `Origin Page ${stamp}`
    const pageBTitle = `Target Page ${stamp}`

    // Two codex spaces, one page in each — the cross-space fixture the whole
    // journey needs.
    const spaceA = await createSpace(page, 'Cross Space Wiki A', 'codex')
    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', pageATitle)
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.getByTestId('wiki-page-title')).toContainText(pageATitle, { timeout: 5000 })

    const spaceB = await createSpace(page, 'Cross Space Wiki B', 'codex')
    await page.getByRole('button', { name: 'New page' }).click()
    await page.fill('#page-title', pageBTitle)
    await page.locator('[role="dialog"] button:has-text("Create Page")').click()
    await expect(page.getByTestId('wiki-page-title')).toContainText(pageBTitle, { timeout: 5000 })

    // Back onto page A — its single page auto-selects — where the panel lives.
    await page.goto(`/codex/${spaceA}`)
    await expect(page.getByTestId('wiki-page-title')).toContainText(pageATitle, { timeout: 10000 })
    await expect(page.getByTestId('relations-section')).toBeVisible()

    // A page's only target is a page, so the add-row goes straight to the
    // picker: no target-type select to switch, and wiki_link is the default.
    await expect(page.getByLabel('Relation target type')).toHaveCount(0)

    const created = page.waitForResponse(
      (r) => r.request().method() === 'POST' && /\/relations$/.test(r.url()),
    )
    await page.getByTestId('relation-page-ref').fill(pageBTitle)
    await page.getByTestId('relation-page-ref-suggestions').getByText(pageBTitle).click()
    const createdResp = await created
    expect(createdResp.status(), 'the page→page relation POST must succeed').toBe(201)
    // The default the C4 change adds reaches the wire as the payload, not just
    // the select's displayed value.
    const body = JSON.parse(createdResp.request().postData() ?? '{}')
    expect(body.to_type).toBe('page')
    expect(body.kind).toBe('wiki_link')

    // Outgoing far side on A: page B's title, linked into B's OWN space.
    const farLink = page.getByTestId('relations-section').getByRole('link', { name: pageBTitle })
    await expect(farLink).toBeVisible({ timeout: 5000 })
    await expect(farLink).toHaveAttribute('href', new RegExp(`/codex/${spaceB}/pages/`))

    await farLink.click()
    await expect(page).toHaveURL(new RegExp(`/codex/${spaceB}/pages/`), { timeout: 10000 })
    await expect(page.getByTestId('wiki-page-title')).toContainText(pageBTitle, { timeout: 5000 })

    // The reciprocal, with no second write: page B's panel shows the incoming
    // link back to A, into A's own space, and it returns there when followed.
    await expect(page.getByTestId('relations-section')).toBeVisible()
    const backLink = page.getByTestId('relations-section').getByRole('link', { name: pageATitle })
    await expect(backLink).toBeVisible({ timeout: 5000 })
    await expect(backLink).toHaveAttribute('href', new RegExp(`/codex/${spaceA}/pages/`))

    await backLink.click()
    await expect(page).toHaveURL(new RegExp(`/codex/${spaceA}/pages/`), { timeout: 10000 })
    await expect(page.getByTestId('wiki-page-title')).toContainText(pageATitle, { timeout: 5000 })
    await assertNoErrors(page)
  })
})
