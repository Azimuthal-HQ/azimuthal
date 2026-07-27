import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'
import { assertNoErrors, createSpace, createUserAndLogin, getAuthToken, getCurrentUser } from './helpers/setup'

/**
 * The Codex document editor (issue #15 PR-B), through a real browser.
 *
 * The centrepiece is `preserved content survives a real edit session` below.
 * ADR-0012 names that scenario as "the requirement most likely to be missed
 * and the most damaging when it is" — a user opens an imported page, fixes a
 * typo, and silently destroys forty macros — and requires it to have an
 * explicit test. PR #73 proved the server round-trips those bytes. This proves
 * nothing between a real ProseMirror and that server loses them.
 */

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

interface Ctx {
  orgId: string
  spaceId: string
  token: string
}

async function codexSpace(page: Page, name: string): Promise<Ctx> {
  await createUserAndLogin(page)
  const { orgId } = await getCurrentUser(page)
  const spaceId = await createSpace(page, name, 'codex')
  const token = await getAuthToken(page)
  return { orgId, spaceId, token }
}

function api(ctx: Ctx) {
  return {
    base: `/api/v1/orgs/${ctx.orgId}/spaces/${ctx.spaceId}/wiki`,
    headers: { Authorization: `Bearer ${ctx.token}`, 'Content-Type': 'application/json' },
  }
}

/** Creates a page with markdown content, returning its id. */
async function createPage(page: Page, ctx: Ctx, title: string, content: string): Promise<string> {
  const { base, headers } = api(ctx)
  const res = await page.request.post(base, { headers, data: { title, content } })
  expect(res.status()).toBe(201)
  return (await res.json()).id as string
}

/** The page as stored — `doc` here is the raw document, not the shielded one. */
async function storedPage(page: Page, ctx: Ctx, pageId: string) {
  const { base, headers } = api(ctx)
  const res = await page.request.get(`${base}/${pageId}`, { headers })
  expect(res.status()).toBe(200)
  return res.json()
}

/** Opens a page in the editor and waits for the surface to be ready. */
async function openEditor(page: Page, ctx: Ctx, pageId: string) {
  await page.goto(`/codex/${ctx.spaceId}/pages/${pageId}`)
  await expect(page.getByTestId('wiki-page-title')).toBeVisible({ timeout: 10000 })
  await page.getByRole('button', { name: 'Edit', exact: true }).click()
  await expect(page.locator('.ProseMirror')).toBeVisible({ timeout: 10000 })
}

// ---------------------------------------------------------------------------
// B2 — the ADR-0012 round trip. The most important test in this phase.
// ---------------------------------------------------------------------------

test.describe('preserved content (ADR-0012)', () => {
  /**
   * The seed is raw HTML inside markdown. `doc.FromMarkdown` maps it to a
   * `legacyHtmlBlock` node, which `schema.json` does not name — so `Shield`
   * captures it and the editor receives a placeholder. That reaches an
   * unrepresentable node through the public API alone, with no fixture
   * reaching into the database, and it is not a contrivance: it is exactly
   * what every page written in the markdown editor that used raw HTML already
   * contains.
   */
  const RAW_HTML = '<div class="callout" data-x="a &amp; b"><b>Escalation path</b></div>'

  test('is shown to the author, labelled, rather than silently dropped', async ({ page }) => {
    const ctx = await codexSpace(page, 'Preserve Label Wiki')
    const pageId = await createPage(page, ctx, 'Imported Page', `Intro paragraph.\n\n${RAW_HTML}\n`)

    await openEditor(page, ctx, pageId)

    const block = page.getByTestId('codex-preserved-block')
    await expect(block).toBeVisible({ timeout: 10000 })
    // Labelled: what it was, and where it came from.
    await expect(block).toContainText('Preserved')
    await expect(block).toContainText('legacyHtmlBlock')
    await expect(block).toContainText('Escalation path')
    // Inert: nothing inside it is typeable.
    await expect(block.locator('[contenteditable="true"]')).toHaveCount(0)
    await assertNoErrors(page)
  })

  test('survives a real edit session byte-identically', async ({ page }) => {
    const ctx = await codexSpace(page, 'Preserve Roundtrip Wiki')
    const pageId = await createPage(page, ctx, 'Imported Page', `Intro paragraph.\n\n${RAW_HTML}\n`)

    // ---- First publish: the conversion writes `doc` for the first time. ----
    await openEditor(page, ctx, pageId)
    await expect(page.getByTestId('codex-preserved-block')).toBeVisible({ timeout: 10000 })
    await page.getByTestId('codex-publish').click()
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 10000 })

    const afterConversion = await storedPage(page, ctx, pageId)
    const preservedBefore = findNode(afterConversion.doc, 'legacyHtmlBlock')
    expect(preservedBefore, 'conversion must have stored the unrepresentable node').not.toBeNull()

    // ---- Now the ADR-0012 scenario: open it, edit AROUND it, publish. ----
    await openEditor(page, ctx, pageId)
    await expect(page.getByTestId('codex-preserved-block')).toBeVisible({ timeout: 10000 })

    const editor = page.locator('.ProseMirror')
    // Click into the first paragraph and fix a "typo" — the innocuous edit
    // that ADR-0012 says destroys forty macros.
    await editor.getByText('Intro paragraph.').click()
    await page.keyboard.press('End')
    await editor.pressSequentially(' Edited by a human.')

    // And add a paragraph after the preserved block, so the document is
    // genuinely re-serialised around it rather than left untouched.
    await page.keyboard.press('Control+End')
    await page.keyboard.press('Enter')
    await editor.pressSequentially('A new closing paragraph.')

    await page.getByTestId('codex-publish').click()
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 10000 })

    // ---- The assertion the whole phase exists for. ----
    const afterEdit = await storedPage(page, ctx, pageId)
    const preservedAfter = findNode(afterEdit.doc, 'legacyHtmlBlock')

    expect(preservedAfter, 'the preserved node must still be there').not.toBeNull()
    // Byte-identical: the same JSON, member for member, including the raw HTML
    // with its angle brackets and entities.
    expect(JSON.stringify(preservedAfter)).toBe(JSON.stringify(preservedBefore))
    expect(JSON.stringify(preservedAfter)).toContain('Escalation path')

    // The edit really did happen — otherwise this test proves only that an
    // unchanged document is unchanged.
    expect(JSON.stringify(afterEdit.doc)).toContain('Edited by a human.')
    expect(JSON.stringify(afterEdit.doc)).toContain('A new closing paragraph.')
    expect(afterEdit.version).toBeGreaterThan(afterConversion.version)

    // And a reader sees it, labelled, on the published page.
    await expect(page.locator('article').getByTestId('codex-preserved-block')).toBeVisible({
      timeout: 10000,
    })
    await assertNoErrors(page)
  })

  test('deleting it is refused until the removal is confirmed by name', async ({ page }) => {
    const ctx = await codexSpace(page, 'Preserve Ack Wiki')
    const pageId = await createPage(page, ctx, 'Imported Page', `Intro paragraph.\n\n${RAW_HTML}\n`)

    // Publish once so the page holds a stored document with preserved content.
    await openEditor(page, ctx, pageId)
    await expect(page.getByTestId('codex-preserved-block')).toBeVisible({ timeout: 10000 })
    await page.getByTestId('codex-publish').click()
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 10000 })

    // Reopen and delete the preserved block: select the atom, then remove it.
    await openEditor(page, ctx, pageId)
    const block = page.getByTestId('codex-preserved-block')
    await expect(block).toBeVisible({ timeout: 10000 })
    await block.click()
    await page.keyboard.press('Backspace')
    await expect(block).toHaveCount(0)

    await page.getByTestId('codex-publish').click()

    // Refused, with the count and the name — not an error toast.
    const dialog = page.getByTestId('codex-lost-content-dialog')
    await expect(dialog).toBeVisible({ timeout: 10000 })
    await expect(page.getByTestId('codex-lost-content-count')).toContainText('1 preserved item')
    await expect(page.getByTestId('codex-lost-content-item').first()).toContainText('legacyHtmlBlock')

    // Cancelling publishes nothing: the page still has its preserved content.
    await page.getByTestId('codex-lost-content-cancel').click()
    await expect(dialog).toBeHidden()
    const stillThere = await storedPage(page, ctx, pageId)
    expect(findNode(stillThere.doc, 'legacyHtmlBlock')).not.toBeNull()

    // Confirming carries the acknowledgement and the removal commits.
    await page.getByTestId('codex-publish').click()
    await expect(dialog).toBeVisible({ timeout: 10000 })
    await page.getByTestId('codex-lost-content-confirm').click()
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 10000 })

    const afterRemoval = await storedPage(page, ctx, pageId)
    expect(findNode(afterRemoval.doc, 'legacyHtmlBlock')).toBeNull()
    await assertNoErrors(page)
  })
})

// ---------------------------------------------------------------------------
// B4 — drafts and publishing
// ---------------------------------------------------------------------------

test.describe('drafts and publishing', () => {
  test('autosaves, restores on return, and readers see only the published version', async ({
    page,
  }) => {
    const ctx = await codexSpace(page, 'Draft Cycle Wiki')
    const pageId = await createPage(page, ctx, 'Draft Page', 'Published body.\n')

    await openEditor(page, ctx, pageId)
    const editor = page.locator('.ProseMirror')
    await editor.click()
    await page.keyboard.press('Control+End')
    await editor.pressSequentially(' Work in progress.')

    // Autosaved, without being asked to save.
    await expect(page.getByTestId('codex-save-state')).toHaveAttribute('data-state', 'saved', {
      timeout: 15000,
    })

    // Leave entirely — a reload, not a route change.
    await page.reload()
    await expect(page.getByTestId('wiki-page-title')).toBeVisible({ timeout: 10000 })

    // A reader (this author, in read mode) still sees the PUBLISHED page.
    await expect(page.locator('article')).not.toContainText('Work in progress.')
    // And is told they have unpublished work here.
    await expect(page.getByTestId('codex-unpublished-badge')).toBeVisible({ timeout: 10000 })

    // The draft is waiting in the Drafts view too.
    await page.goto(`/codex/${ctx.spaceId}/drafts`)
    await expect(page.getByTestId('codex-draft-row').first()).toContainText('Draft Page', {
      timeout: 10000,
    })

    // Coming back restores it.
    await openEditor(page, ctx, pageId)
    await expect(page.getByTestId('codex-draft-restored')).toBeVisible({ timeout: 10000 })
    await expect(page.locator('.ProseMirror')).toContainText('Work in progress.')

    // Publishing makes it a reader's problem at last.
    await page.getByTestId('codex-publish').click()
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 10000 })
    await expect(page.locator('article')).toContainText('Work in progress.')
    await expect(page.getByTestId('codex-unpublished-badge')).toHaveCount(0)
    await assertNoErrors(page)
  })

  test('discarding a draft is confirmed and leaves the published page alone', async ({ page }) => {
    const ctx = await codexSpace(page, 'Discard Draft Wiki')
    const pageId = await createPage(page, ctx, 'Discard Page', 'Published body.\n')

    await openEditor(page, ctx, pageId)
    const editor = page.locator('.ProseMirror')
    await editor.click()
    await page.keyboard.press('Control+End')
    await editor.pressSequentially(' Throwaway text.')
    await expect(page.getByTestId('codex-save-state')).toHaveAttribute('data-state', 'saved', {
      timeout: 15000,
    })

    await page.getByTestId('codex-discard-draft').click()
    await expect(page.getByTestId('codex-discard-dialog')).toBeVisible()
    await page.getByTestId('codex-discard-confirm').click()

    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 10000 })
    await expect(page.getByTestId('codex-unpublished-badge')).toHaveCount(0)
    await expect(page.locator('article')).toContainText('Published body.')
    await expect(page.locator('article')).not.toContainText('Throwaway text.')
    await assertNoErrors(page)
  })

  test('a publish conflict offers reload and overwrite, and both work', async ({ page }) => {
    const ctx = await codexSpace(page, 'Conflict Wiki')
    const pageId = await createPage(page, ctx, 'Contested Page', 'Original body.\n')
    const { base, headers } = api(ctx)

    // --- Reload arm -------------------------------------------------------
    await openEditor(page, ctx, pageId)
    const editor = page.locator('.ProseMirror')
    await editor.click()
    await page.keyboard.press('Control+End')
    await editor.pressSequentially(' My edit.')
    await expect(page.getByTestId('codex-save-state')).toHaveAttribute('data-state', 'saved', {
      timeout: 15000,
    })

    // Somebody else publishes underneath. The markdown save path is the
    // simplest way to move the page's version from outside this session.
    const current = await storedPage(page, ctx, pageId)
    const bump = await page.request.put(`${base}/${pageId}`, {
      headers,
      data: {
        title: 'Contested Page',
        content: 'Somebody else got here first.\n',
        expected_version: current.version,
      },
    })
    expect(bump.status()).toBe(200)

    await page.getByTestId('codex-publish').click()
    const conflict = page.getByTestId('codex-conflict-dialog')
    await expect(conflict).toBeVisible({ timeout: 10000 })
    await expect(page.getByTestId('codex-conflict-message')).toContainText('version')

    await page.getByTestId('codex-conflict-reload').click()
    await expect(conflict).toBeHidden({ timeout: 10000 })
    // The newer version is now in the editor…
    await expect(page.locator('.ProseMirror')).toContainText('Somebody else got here first.', {
      timeout: 10000,
    })
    // …and the draft survived: it is still listed as unpublished work.
    const drafts = await page.request.get(`${base}/drafts`, { headers })
    expect(drafts.status()).toBe(200)
    expect((await drafts.json()).some((d: { page_id: string }) => d.page_id === pageId)).toBe(true)

    // --- Overwrite arm ----------------------------------------------------
    // Make an edit on top of the reloaded version, then have somebody else
    // publish again, so publishing conflicts a second time.
    await page.locator('.ProseMirror').click()
    await page.keyboard.press('Control+End')
    await page.locator('.ProseMirror').pressSequentially(' Mine wins.')
    await expect(page.getByTestId('codex-save-state')).toHaveAttribute('data-state', 'saved', {
      timeout: 15000,
    })

    const current2 = await storedPage(page, ctx, pageId)
    const bump2 = await page.request.put(`${base}/${pageId}`, {
      headers,
      data: {
        title: 'Contested Page',
        content: 'And again.\n',
        expected_version: current2.version,
      },
    })
    expect(bump2.status()).toBe(200)

    await page.getByTestId('codex-publish').click()
    await expect(conflict).toBeVisible({ timeout: 10000 })
    await page.getByTestId('codex-conflict-overwrite').click()

    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 15000 })
    await expect(page.locator('article')).toContainText('Mine wins.')
    await assertNoErrors(page)
  })
})

// ---------------------------------------------------------------------------
// B1/B3/B5 — the editing surface itself
// ---------------------------------------------------------------------------

test.describe('the editing surface', () => {
  test('converts a legacy markdown page on first edit, losing nothing', async ({ page }) => {
    const ctx = await codexSpace(page, 'Conversion Wiki')
    const markdown = [
      '# Heading one',
      '',
      'A paragraph with **bold** and `code`.',
      '',
      '- first bullet',
      '- second bullet',
      '',
      '> a quotation',
      '',
      '```go',
      'func main() {}',
      '```',
      '',
      '| a | b |',
      '| --- | --- |',
      '| 1 | 2 |',
      '',
    ].join('\n')
    const pageId = await createPage(page, ctx, 'Legacy Page', markdown)

    // Read mode still uses the markdown path, unchanged — `doc` is null.
    await page.goto(`/codex/${ctx.spaceId}/pages/${pageId}`)
    await expect(page.locator('article')).toContainText('Heading one', { timeout: 10000 })
    const before = await storedPage(page, ctx, pageId)
    expect(before.doc).toBeFalsy()

    // Opening the editor converts it, and publishing writes the document.
    await page.getByRole('button', { name: 'Edit', exact: true }).click()
    const editor = page.locator('.ProseMirror')
    await expect(editor).toBeVisible({ timeout: 10000 })

    for (const text of ['Heading one', 'first bullet', 'a quotation', 'func main() {}']) {
      await expect(editor).toContainText(text)
    }
    await expect(editor.locator('table')).toBeVisible()

    await page.getByTestId('codex-publish').click()
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 10000 })

    const after = await storedPage(page, ctx, pageId)
    expect(after.doc).toBeTruthy()
    const serialised = JSON.stringify(after.doc)
    for (const fragment of ['Heading one', 'first bullet', 'a quotation', 'func main() {}']) {
      expect(serialised).toContain(fragment)
    }
    // The markdown projection is still written, so search keeps working.
    expect(after.content).toContain('Heading one')

    // And the reader now gets the document renderer, with the content intact.
    await expect(page.locator('article')).toContainText('Heading one')
    await expect(page.locator('article')).toContainText('first bullet')
    await assertNoErrors(page)
  })

  test('inserts and edits a macro, and it survives a publish', async ({ page }) => {
    const ctx = await codexSpace(page, 'Macro Wiki')
    const pageId = await createPage(page, ctx, 'Macro Page', 'Body.\n')

    await openEditor(page, ctx, pageId)
    const editor = page.locator('.ProseMirror')
    await editor.click()
    await page.keyboard.press('Control+End')

    // Panel, from the toolbar, then change its kind — the attribute the
    // markdown projection reads.
    await page.getByTestId('codex-tool-panel').click()
    const panel = page.getByTestId('codex-panel')
    await expect(panel).toBeVisible({ timeout: 10000 })
    await panel.getByTestId('codex-panel-kind').selectOption('warning')
    await expect(panel).toHaveAttribute('data-kind', 'warning')

    // A status lozenge, whose label is its whole content.
    await editor.click()
    await page.keyboard.press('Control+End')
    await page.getByTestId('codex-tool-status').click()
    const lozenge = page.getByTestId('codex-status-lozenge')
    await expect(lozenge).toBeVisible()
    await page.getByTestId('codex-status-lozenge-label').click()
    const input = page.getByTestId('codex-status-lozenge-input')
    await input.fill('IN REVIEW')
    await page.keyboard.press('Enter')
    await expect(lozenge).toContainText('IN REVIEW')

    await page.getByTestId('codex-publish').click()
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 10000 })

    // Stored with the attributes intact…
    const stored = await storedPage(page, ctx, pageId)
    const serialised = JSON.stringify(stored.doc)
    expect(serialised).toContain('"panel"')
    expect(serialised).toContain('"warning"')
    expect(serialised).toContain('IN REVIEW')
    // …and projected into the markdown that feeds search, which is what those
    // attribute names are for.
    expect(stored.content).toContain('WARNING')
    expect(stored.content).toContain('IN REVIEW')

    // The reader sees the macro rendered, not a placeholder for one.
    await expect(page.locator('article').getByTestId('codex-panel')).toHaveAttribute(
      'data-kind',
      'warning',
    )
    await assertNoErrors(page)
  })

  test('the toolbar is operable from the keyboard alone', async ({ page }) => {
    const ctx = await codexSpace(page, 'A11y Wiki')
    const pageId = await createPage(page, ctx, 'Keyboard Page', 'Body.\n')

    await openEditor(page, ctx, pageId)
    const toolbar = page.getByTestId('codex-toolbar')
    await expect(toolbar).toHaveAttribute('role', 'toolbar')

    // One tab stop, arrow keys within — the ARIA toolbar pattern. Without it,
    // reaching the editor's content means ~20 tab presses.
    await page.getByTestId('codex-tool-h1').focus()
    await page.keyboard.press('ArrowRight')
    await expect(page.getByTestId('codex-tool-h2')).toBeFocused()
    await page.keyboard.press('End')
    await page.keyboard.press('Home')
    await expect(page.getByTestId('codex-tool-h1')).toBeFocused()

    // And it reports state, not just styling.
    await page.locator('.ProseMirror').click()
    await page.keyboard.press('Control+End')
    await page.getByTestId('codex-tool-bold').click()
    await expect(page.getByTestId('codex-tool-bold')).toHaveAttribute('aria-pressed', 'true')
    await assertNoErrors(page)
  })

  test('markdown input rules still work while typing', async ({ page }) => {
    const ctx = await codexSpace(page, 'Input Rules Wiki')
    const pageId = await createPage(page, ctx, 'Rules Page', '')

    await openEditor(page, ctx, pageId)
    const editor = page.locator('.ProseMirror')
    await editor.click()

    await editor.pressSequentially('## A typed heading')
    await page.keyboard.press('Enter')
    await editor.pressSequentially('- a typed bullet')

    await expect(editor.locator('h2')).toContainText('A typed heading')
    await expect(editor.locator('ul li')).toContainText('a typed bullet')

    await page.getByTestId('codex-publish').click()
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 10000 })
    await expect(page.locator('article').locator('h2')).toContainText('A typed heading')
    await assertNoErrors(page)
  })
})

// ---------------------------------------------------------------------------
// B5 — images
// ---------------------------------------------------------------------------

test.describe('images', () => {
  /** A real 1×1 PNG. The server sniffs the bytes, so it has to be one. */
  const PNG = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
    'base64',
  )

  test('uploads an image, stores it by id, and shows it to a reader', async ({ page }) => {
    const ctx = await codexSpace(page, 'Image Wiki')
    const pageId = await createPage(page, ctx, 'Image Page', 'Body.\n')

    await openEditor(page, ctx, pageId)
    await page.locator('.ProseMirror').click()
    await page.keyboard.press('Control+End')

    await page.getByTestId('codex-image-input').setInputFiles({
      name: 'diagram.png',
      mimeType: 'image/png',
      buffer: PNG,
    })

    const image = page.getByTestId('codex-image')
    await expect(image).toBeVisible({ timeout: 15000 })
    // Addressed by attachment id, never by a baked-in URL: the address a
    // reader needs depends on whether they came through the space or a share.
    await expect(image).toHaveAttribute('data-attachment-id', /[0-9a-f-]{36}/)

    await page.getByTestId('codex-image-alt').fill('A network diagram')

    await page.getByTestId('codex-publish').click()
    await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 15000 })

    const stored = await storedPage(page, ctx, pageId)
    const serialised = JSON.stringify(stored.doc)
    expect(serialised).toContain('attachment_id')
    expect(serialised).toContain('A network diagram')
    // The markdown projection addresses it the same way.
    expect(stored.content).toContain('attachment:')

    // A reader gets the bytes. The image is fetched through the authenticated
    // client and shown from an object URL, because the attachment route wants
    // a credential a bare <img src> cannot send.
    const readerImage = page.locator('article').getByTestId('codex-image').locator('img')
    await expect(readerImage).toBeVisible({ timeout: 15000 })
    await expect(readerImage).toHaveAttribute('src', /^blob:/)
    await expect(readerImage).toHaveAttribute('alt', 'A network diagram')
    await assertNoErrors(page)
  })

  test('refuses a file that is not an image, and says so without breaking the editor', async ({
    page,
  }) => {
    const ctx = await codexSpace(page, 'Image Reject Wiki')
    const pageId = await createPage(page, ctx, 'Reject Page', 'Body.\n')

    await openEditor(page, ctx, pageId)
    await page.locator('.ProseMirror').click()

    // Declares image/png; the bytes are plainly not. The server sniffs, so
    // the declaration buys nothing — which is the point.
    await page.getByTestId('codex-image-input').setInputFiles({
      name: 'not-an-image.png',
      mimeType: 'image/png',
      buffer: Buffer.from('#!/bin/sh\necho definitely not a png\n'),
    })

    await expect(page.getByTestId('codex-editor-error')).toBeVisible({ timeout: 15000 })
    await expect(page.getByTestId('codex-editor-error')).toContainText(/PNG, JPEG, WebP or GIF/i)
    // No image node was inserted, and the editor still works.
    await expect(page.getByTestId('codex-image')).toHaveCount(0)
    await page.locator('.ProseMirror').click()
    await page.keyboard.press('Control+End')
    await page.locator('.ProseMirror').pressSequentially(' still typing.')
    await expect(page.locator('.ProseMirror')).toContainText('still typing.')
    await assertNoErrors(page)
  })
})

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** The first node of the given type anywhere in a stored document, or null. */
function findNode(node: unknown, type: string): unknown {
  if (node == null || typeof node !== 'object') return null
  const candidate = node as { type?: string; content?: unknown[]; marks?: unknown[] }
  if (candidate.type === type) return node
  for (const member of [candidate.content, candidate.marks]) {
    for (const child of member ?? []) {
      const found = findNode(child, type)
      if (found) return found
    }
  }
  return null
}
