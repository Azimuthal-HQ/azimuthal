import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'
import { assertNoErrors, createSpace, createUserAndLogin, getAuthToken, getCurrentUser } from './helpers/setup'

/**
 * The Obsidian-shaped authoring affordances, through a real browser: markdown
 * that pastes as structure, `[[wikilinks]]`, and inline `#tags`.
 *
 * Every one of these features is a shortcut over machinery that already
 * shipped — a paste becomes ordinary document nodes, a wikilink becomes the
 * existing link mark, a `#tag` becomes a page tag. So the interesting question
 * is never "did something appear on screen": a contentEditable region will
 * happily show whatever was typed into it whether or not a single byte reached
 * the server. Each journey below therefore reads the page back through the API
 * and asserts the STORED document, exactly as codex-editor.spec.ts does for the
 * ADR-0012 round trip.
 *
 * Two of these tests are negatives, and they are the ones worth keeping. Prose
 * containing a hash must paste unchanged, and deleting the last inline `#tag`
 * must NOT untag the page. Both encode decisions that a plausible "improvement"
 * would quietly reverse.
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

/**
 * The page's tags as the server holds them.
 *
 * Read through the API rather than off the chips, because the chips are a
 * cached query and the question these tests ask is what the SERVER did at
 * publish. See the note in `an inline #tag is aggregated…` for why the two can
 * legitimately disagree for a moment.
 */
async function storedTags(page: Page, ctx: Ctx, pageId: string): Promise<{ name: string; slug: string }[]> {
  const { base, headers } = api(ctx)
  const res = await page.request.get(`${base}/${pageId}/tags`, { headers })
  expect(res.status()).toBe(200)
  return (await res.json()) ?? []
}

/** Opens a page in the editor and waits for the surface to be ready. */
async function openEditor(page: Page, ctx: Ctx, pageId: string) {
  await page.goto(`/codex/${ctx.spaceId}/pages/${pageId}`)
  await expect(page.getByTestId('wiki-page-title')).toBeVisible({ timeout: 10000 })
  await page.getByRole('button', { name: 'Edit', exact: true }).click()
  await expect(page.locator('.ProseMirror')).toBeVisible({ timeout: 10000 })
}

/** Publishes and waits for the read-mode surface to come back. */
async function publish(page: Page) {
  await page.getByTestId('codex-publish').click()
  await expect(page.getByRole('button', { name: 'Edit', exact: true })).toBeVisible({ timeout: 15000 })
}

/**
 * Pastes plain text into the editor.
 *
 * A synthetic `paste` event carrying a DataTransfer rather than the OS
 * clipboard, and that is a deliberate choice rather than a convenience. The
 * handler under test (`CodexEditor`'s `editorProps.handlePaste`) reads exactly
 * two things off the event — `text/html` and `text/plain` — so this exercises
 * the real path with nothing left to a shared, machine-wide clipboard that four
 * parallel workers would be writing to at once.
 *
 * The event is dispatched on `.ProseMirror` because that is the node
 * prosemirror-view registers its paste handler on, and because
 * `eventBelongsToView` refuses events whose target is outside the editor.
 */
async function pasteText(page: Page, text: string): Promise<void> {
  const editor = page.locator('.ProseMirror')
  await editor.click()
  await page.keyboard.press('Control+End')
  await editor.evaluate((element, payload) => {
    const transfer = new DataTransfer()
    // text/plain only. Setting text/html as well would make the handler defer
    // to ProseMirror's own parser, which is the correct behaviour for a paste
    // from a browser or a word processor — and not the path being tested.
    transfer.setData('text/plain', payload)
    element.dispatchEvent(
      new ClipboardEvent('paste', { clipboardData: transfer, bubbles: true, cancelable: true }),
    )
  }, text)
}

/**
 * A tag label nothing else in the org will be using.
 *
 * Tags are ORG-scoped and every seeded user lands in the same org (see
 * `seedUser`: the display name is the org key), so a fixed label like
 * `runbooks` would collect pages from every other run of this suite and make
 * "the tag page lists my page" an assertion about the whole database. Letters
 * only, so the server's `Slugify` — lowercase, non-alphanumerics to
 * underscores — returns the label unchanged and the `data-slug` the chip
 * renders is predictable without reimplementing the slug rule here.
 */
function uniqueTagLabel(prefix: string): string {
  const letters = 'abcdefghijklmnopqrstuvwxyz'
  let suffix = ''
  for (let i = 0; i < 8; i += 1) suffix += letters[Math.floor(Math.random() * letters.length)]
  return `${prefix}${suffix}`
}

// ---------------------------------------------------------------------------
// Pasted markdown
// ---------------------------------------------------------------------------

test.describe('pasted markdown', () => {
  const MARKDOWN = [
    '# Deployment checklist',
    '',
    '- Drain the node',
    '- Roll the pods',
    '',
    '```bash',
    'kubectl drain node-1',
    '```',
    '',
    'Everything above is **load-bearing**.',
    '',
  ].join('\n')

  test('pasted markdown becomes structure, and publishes as structure', async ({ page }) => {
    const ctx = await codexSpace(page, 'Paste Markdown Wiki')
    const pageId = await createPage(page, ctx, 'Runbook Draft', '')

    await openEditor(page, ctx, pageId)
    await pasteText(page, MARKDOWN)

    // On screen first: four different constructs, so a converter that handled
    // only headings would not satisfy this.
    const editor = page.locator('.ProseMirror')
    await expect(editor.locator('h1')).toContainText('Deployment checklist', { timeout: 10000 })
    await expect(editor.locator('ul li')).toHaveCount(2)
    await expect(page.getByTestId('codex-code-block')).toBeVisible()
    await expect(editor.locator('strong')).toContainText('load-bearing')

    await publish(page)

    // The assertion that matters: the STORED document holds real nodes. The
    // editor is contentEditable, so every check above would also pass against a
    // paste that had landed as a single paragraph of literal markdown text with
    // the right words in it.
    const stored = await storedPage(page, ctx, pageId)
    const heading = findNode(stored.doc, 'heading')
    expect(heading, 'the paste must have produced a heading node').not.toBeNull()
    expect(attrOf(heading, 'level')).toBe(1)

    const code = findNode(stored.doc, 'codeBlock')
    expect(code, 'the fence must have produced a codeBlock node').not.toBeNull()
    expect(JSON.stringify(code)).toContain('kubectl drain node-1')

    expect(findNode(stored.doc, 'bulletList'), 'the bullets must be a list').not.toBeNull()
    expect(findNode(stored.doc, 'bold'), 'the emphasis must be a bold mark').not.toBeNull()

    // And no literal marker survived as text anywhere — a paste that had been
    // left alone would still contain "# Deployment checklist".
    expect(JSON.stringify(stored.doc)).not.toContain('# Deployment checklist')

    // The markdown projection is written too, so the pasted content is
    // searchable rather than only readable.
    expect(stored.content).toContain('Deployment checklist')
    await assertNoErrors(page)
  })

  test('prose containing a hash pastes unchanged', async ({ page }) => {
    // The negative half, and the more important one. `looksLikeMarkdown` is
    // deliberately conservative because a false negative costs a paste that
    // arrives as plain text — which is what happened before this feature
    // existed — while a false positive rewrites somebody's sentence into a
    // structure they did not ask for and cannot easily undo.
    const ctx = await codexSpace(page, 'Paste Prose Wiki')
    const pageId = await createPage(page, ctx, 'Meeting Notes', '')

    await openEditor(page, ctx, pageId)
    await pasteText(page, 'See issue #42 in the tracker.')

    const editor = page.locator('.ProseMirror')
    await expect(editor).toContainText('See issue #42 in the tracker.', { timeout: 10000 })
    // Not a heading, on screen: a converter that read the `#` positionally
    // would have made one out of the whole sentence.
    await expect(editor.locator('h1')).toHaveCount(0)

    await publish(page)

    const stored = await storedPage(page, ctx, pageId)
    expect(findNode(stored.doc, 'heading'), 'prose must not become a heading').toBeNull()
    // Nor a tag. `#42` is the commonest hash in ordinary prose, and minting an
    // org-scoped tag called "42" from one would put it in every autocomplete
    // for everyone, with no administration surface to remove it.
    expect(findNode(stored.doc, 'inlineTag'), 'an issue number is not a tag').toBeNull()
    // The text survives character for character, hash included.
    expect(JSON.stringify(stored.doc)).toContain('See issue #42 in the tracker.')
    await assertNoErrors(page)
  })
})

// ---------------------------------------------------------------------------
// Wikilinks
// ---------------------------------------------------------------------------

test.describe('wikilinks', () => {
  test('typing [[ offers pages and links to the one chosen', async ({ page }) => {
    const ctx = await codexSpace(page, 'Wikilink Pick Wiki')
    const targetId = await createPage(page, ctx, 'Escalation Runbook', 'The steps.\n')
    const sourceId = await createPage(page, ctx, 'Duty Notes', 'Notes.\n')

    await openEditor(page, ctx, sourceId)
    const editor = page.locator('.ProseMirror')
    await editor.click()
    await page.keyboard.press('Control+End')
    await editor.pressSequentially('See [[Escalation')

    const popup = page.getByTestId('codex-wikilink-suggestions')
    await expect(popup).toBeVisible({ timeout: 10000 })
    const option = page.getByTestId('codex-wikilink-option').first()
    await expect(option).toContainText('Escalation Runbook')
    await option.click()

    // Resolved to that page specifically, not merely to something. An
    // autocomplete that inserted the title as an unresolved reference would
    // look identical to a reader until they clicked it.
    await expect(editor.locator(`a[data-page-id="${targetId}"]`)).toHaveCount(1)
    await expect(popup).toHaveCount(0)

    await publish(page)

    const stored = await storedPage(page, ctx, sourceId)
    const link = findNode(stored.doc, 'link')
    expect(link, 'the reference must be stored as a link mark').not.toBeNull()
    expect(attrOf(link, 'page_id')).toBe(targetId)
    // Never an href: a page's URL depends on the space it is being read in, so
    // a document that baked one in would be wrong for anyone who arrived
    // through a share rather than through the space.
    expect(attrOf(link, 'href')).toBeNull()
    expect(attrOf(link, 'target_title')).toBeNull()

    // From the READING surface — where the click is a route, not an href.
    const readerLink = page.locator('article').locator(`a[data-page-id="${targetId}"]`)
    await expect(readerLink).toBeVisible({ timeout: 10000 })
    await readerLink.click()
    await expect(page).toHaveURL(new RegExp(`/codex/${ctx.spaceId}/pages/${targetId}$`), {
      timeout: 10000,
    })
    await expect(page.getByTestId('wiki-page-title')).toContainText('Escalation Runbook')
    await assertNoErrors(page)
  })

  test('a wikilink to a page nobody has written offers to create it, and the link then resolves', async ({
    page,
  }) => {
    // Writing the link first and making the page later is how a wiki grows, so
    // an unmatched target is a state rather than a refusal. This is the whole
    // journey of that state: written, stored, visibly distinct, then resolved.
    const ctx = await codexSpace(page, 'Wikilink Unresolved Wiki')
    const pageId = await createPage(page, ctx, 'Duty Log', 'Notes.\n')

    await openEditor(page, ctx, pageId)
    const editor = page.locator('.ProseMirror')
    await editor.click()
    await page.keyboard.press('Control+End')
    await editor.pressSequentially('See [[Incident review]]')

    // The input rule fires on the closing bracket, at type time — this is the
    // guarantee that does not depend on the popup having been used.
    await expect(editor.locator('a[data-unresolved]')).toHaveCount(1, { timeout: 10000 })
    await editor.pressSequentially('before escalating.')

    await publish(page)

    const stored = await storedPage(page, ctx, pageId)
    const link = findNode(stored.doc, 'link')
    expect(link, 'the unresolved reference must still be a link mark').not.toBeNull()
    expect(attrOf(link, 'target_title')).toBe('Incident review')
    // Exactly one of the two, or the reading surface cannot tell which of the
    // three link states it is looking at.
    expect(attrOf(link, 'page_id')).toBeNull()

    const readerLink = page.locator('article').locator('a[data-unresolved]')
    await expect(readerLink).toBeVisible({ timeout: 10000 })
    await expect(readerLink).toHaveAttribute('data-target-title', 'Incident review')

    await readerLink.click()
    const dialog = page.getByTestId('codex-unresolved-link-dialog')
    await expect(dialog).toBeVisible({ timeout: 10000 })
    await page.getByTestId('codex-unresolved-link-create').click()

    // A different page, with the title the author wrote inside the brackets.
    //
    // The "not the page we came from" assertion has to be the WAITING form. A
    // pattern matching the shape `/codex/{uuid}/pages/{uuid}` also matches the
    // page being navigated away FROM, so it is satisfied instantly by the
    // pre-navigation URL — and a `page.url()` read straight afterwards
    // snapshots the old one while the router is still committing. Waiting for
    // the old id to be gone is what actually observes the navigation.
    await expect(page, 'creating the page must navigate away from the linking page').not.toHaveURL(
      new RegExp(pageId),
      { timeout: 10000 },
    )
    await expect(page).toHaveURL(/\/codex\/[0-9a-f-]{36}\/pages\/[0-9a-f-]{36}$/, { timeout: 10000 })
    await expect(page.getByTestId('wiki-page-title')).toContainText('Incident review', {
      timeout: 10000,
    })
    await assertNoErrors(page)
  })
})

// ---------------------------------------------------------------------------
// Inline tags
// ---------------------------------------------------------------------------

test.describe('inline tags', () => {
  test('an inline #tag is aggregated onto the page at publish and the tag page lists it', async ({
    page,
  }) => {
    const label = uniqueTagLabel('runbooks')
    const ctx = await codexSpace(page, 'Inline Tag Wiki')
    const pageId = await createPage(page, ctx, 'Failover Drill', 'Notes.\n')

    await openEditor(page, ctx, pageId)
    const editor = page.locator('.ProseMirror')
    await editor.click()
    await page.keyboard.press('Control+End')
    // The TRAILING SPACE is what fires the rule. `#design` on its own is still
    // text — firing on the word itself would convert it the moment it was
    // complete and leave no way to type the literal characters.
    await editor.pressSequentially(` Filed under #${label} `)

    const token = page.getByTestId('codex-inline-tag')
    await expect(token).toBeVisible({ timeout: 10000 })
    await expect(token).toHaveAttribute('data-label', label)

    await publish(page)

    // The load-bearing assertion, and it is about the SERVER. The token in the
    // body is client-side; the page tag is aggregation the publish transaction
    // performed, and this reads it back through the API where no cache can
    // stand in for it.
    const tags = await storedTags(page, ctx, pageId)
    expect(
      tags.map((t) => t.slug),
      'publishing must have added the body tag to the page',
    ).toContain(label)

    // A reader sees it as a chip. The reload is not decoration: `usePublishPage`
    // does not invalidate the page-tags query, and the cached (pre-publish,
    // empty) list has a five-minute staleTime — so the chip a reader sees on a
    // fresh load is the only honest way to assert this from the UI, and it
    // cannot have come from the editing session's own state.
    await page.reload()
    await expect(page.locator(`[data-testid="codex-page-tag"][data-slug="${label}"]`)).toBeVisible({
      timeout: 10000,
    })

    // And the tag browses to the page. The label is unique to this run, so the
    // count is exact rather than a "contains" over whatever else the org holds.
    await page.goto(`/codex/${ctx.spaceId}/tags/${label}`)
    await expect(page.getByTestId('codex-tag-page')).toBeVisible({ timeout: 10000 })
    const rows = page.getByTestId('codex-tag-page-row')
    await expect(rows).toHaveCount(1, { timeout: 10000 })
    await expect(rows.first()).toContainText('Failover Drill')
    await assertNoErrors(page)
  })

  test('removing the last inline #tag does not untag the page', async ({ page }) => {
    // A decided semantic, not an accident: the page-level list is the authority
    // and an inline token is a shortcut into it. Somebody deleting a `#tag`
    // from their prose has edited their prose, not filed the page differently —
    // and the tag editor says so in as many words. This test is what stops the
    // next person making aggregation authoritative, which would silently untag
    // pages on an unrelated edit.
    const label = uniqueTagLabel('oncall')
    const ctx = await codexSpace(page, 'Tag Persistence Wiki')
    const pageId = await createPage(page, ctx, 'Rotation Notes', 'Notes.\n')

    // ---- Establish the tag, the way an author would. --------------------
    await openEditor(page, ctx, pageId)
    const editor = page.locator('.ProseMirror')
    await editor.click()
    await page.keyboard.press('Control+End')
    await editor.pressSequentially(` Filed under #${label} `)
    await expect(page.getByTestId('codex-inline-tag')).toHaveAttribute('data-label', label, {
      timeout: 10000,
    })
    await publish(page)
    expect(
      (await storedTags(page, ctx, pageId)).map((t) => t.slug),
      'the first publish must have tagged the page',
    ).toContain(label)

    // ---- Now delete the whole body and publish that. --------------------
    await openEditor(page, ctx, pageId)
    await page.locator('.ProseMirror').click()
    await page.keyboard.press('Control+A')
    await page.keyboard.press('Backspace')
    await expect(page.getByTestId('codex-inline-tag')).toHaveCount(0)
    await publish(page)

    // The body really is empty of tokens — without this the test would pass
    // against an edit that never happened, which is the failure mode a
    // persistence assertion is most prone to.
    const stored = await storedPage(page, ctx, pageId)
    expect(findNode(stored.doc, 'inlineTag'), 'the token must be gone from the document').toBeNull()

    // …and the page is still tagged.
    expect(
      (await storedTags(page, ctx, pageId)).map((t) => t.slug),
      'deleting the token must not untag the page',
    ).toContain(label)

    await page.reload()
    await expect(page.locator(`[data-testid="codex-page-tag"][data-slug="${label}"]`)).toBeVisible({
      timeout: 10000,
    })
    await expect(page.locator('article').getByTestId('codex-inline-tag')).toHaveCount(0)
    await assertNoErrors(page)
  })
})

// ---------------------------------------------------------------------------
// The shared measure
// ---------------------------------------------------------------------------

test.describe('the document measure', () => {
  test('the reader and the editor share one measure, and it reflows with the window', async ({
    page,
  }) => {
    // Before this, the two surfaces differed as much as two surfaces can: the
    // reader was pinned to a fixed width and the editor had no constraint at
    // all, so pressing Edit rebroke every line in the document. One token, one
    // class, both surfaces.
    const ctx = await codexSpace(page, 'Measure Wiki')
    const pageId = await createPage(page, ctx, 'Wide Page', 'Some body text.\n')

    await page.goto(`/codex/${ctx.spaceId}/pages/${pageId}`)
    await expect(page.getByTestId('wiki-page-title')).toBeVisible({ timeout: 10000 })
    const readMeasure = page.getByTestId('codex-measure')
    await expect(readMeasure).toBeVisible()
    const readBox = await readMeasure.boundingBox()
    expect(readBox).not.toBeNull()

    await page.getByRole('button', { name: 'Edit', exact: true }).click()
    await expect(page.locator('.ProseMirror')).toBeVisible({ timeout: 10000 })
    const editMeasure = page.getByTestId('codex-measure')
    await expect(editMeasure).toBeVisible()
    const editBox = await editMeasure.boundingBox()
    expect(editBox).not.toBeNull()

    // The same measure. The tolerance is for a scrollbar: the two surfaces have
    // different content heights, so one may show a vertical scrollbar the other
    // does not, and that is layout width the measure never asked for. It is
    // generous enough to absorb that and nowhere near enough to absorb the
    // fixed-versus-unconstrained difference this replaced, which was hundreds
    // of pixels.
    expect(
      Math.abs(editBox!.width - readBox!.width),
      'pressing Edit must not change the width the document is laid out in',
    ).toBeLessThanOrEqual(24)

    // Fluid, not fixed. A `max-width` on a `w-full` element reflows during a
    // window drag with nothing subscribed to anything; a fixed width does not
    // move at all, which is exactly what this distinguishes.
    await page.setViewportSize({ width: 640, height: 800 })
    await expect
      .poll(async () => (await page.getByTestId('codex-measure').boundingBox())?.width ?? 0, {
        timeout: 10000,
      })
      .toBeLessThan(editBox!.width - 100)
    await assertNoErrors(page)
  })
})

// ---------------------------------------------------------------------------
// Preserved content, with the new vocabulary (ADR-0012)
// ---------------------------------------------------------------------------

test.describe('preserved content meets the new vocabulary (ADR-0012)', () => {
  /**
   * The same seed codex-editor.spec.ts uses: raw HTML inside markdown becomes a
   * `legacyHtmlBlock`, which `schema.json` does not name, so `Shield` captures
   * it and the editor receives a placeholder. It reaches an unrepresentable node
   * through the public API alone, and it is not a contrivance — it is what every
   * page written in the old markdown editor with raw HTML in it already holds.
   */
  const RAW_HTML = '<div class="callout" data-x="a &amp; b"><b>Escalation path</b></div>'

  test('a document carrying tags, a wikilink and preserved content survives an edit byte-identically', async ({
    page,
  }) => {
    // ADR-0012's guarantee predates tags and wikilinks, and adding vocabulary to
    // a document model is precisely when a preservation guarantee breaks: the
    // new nodes are inserted into the same paragraph, the whole document is
    // re-serialised around the preserved block, and publish re-resolves every
    // placeholder against the version the session started from. If any of that
    // went wrong the bytes would change, and nothing else in the suite would
    // notice — the page would still render, just without forty macros.
    const label = uniqueTagLabel('migrated')
    const ctx = await codexSpace(page, 'Preserve Vocabulary Wiki')
    const pageId = await createPage(page, ctx, 'Imported Page', `Intro paragraph.\n\n${RAW_HTML}\n`)

    // ---- First publish: the conversion writes `doc` for the first time. ----
    await openEditor(page, ctx, pageId)
    await expect(page.getByTestId('codex-preserved-block')).toBeVisible({ timeout: 10000 })
    await publish(page)

    const afterConversion = await storedPage(page, ctx, pageId)
    const preservedBefore = findNode(afterConversion.doc, 'legacyHtmlBlock')
    expect(preservedBefore, 'conversion must have stored the unrepresentable node').not.toBeNull()

    // ---- Now edit around it, using the vocabulary this phase added. ----
    await openEditor(page, ctx, pageId)
    await expect(page.getByTestId('codex-preserved-block')).toBeVisible({ timeout: 10000 })

    const editor = page.locator('.ProseMirror')
    await editor.getByText('Intro paragraph.').click()
    await page.keyboard.press('End')
    await editor.pressSequentially(` Filed under #${label} `)
    await expect(page.getByTestId('codex-inline-tag')).toHaveAttribute('data-label', label, {
      timeout: 10000,
    })
    await editor.pressSequentially('and see [[Incident review]]')
    await expect(editor.locator('a[data-unresolved]')).toHaveCount(1)

    await publish(page)

    // ---- The assertion the whole guarantee rests on. ----
    const afterEdit = await storedPage(page, ctx, pageId)
    const preservedAfter = findNode(afterEdit.doc, 'legacyHtmlBlock')

    expect(preservedAfter, 'the preserved node must still be there').not.toBeNull()
    // Byte-identical: the same JSON, member for member, including the raw HTML
    // with its angle brackets and entities.
    expect(JSON.stringify(preservedAfter)).toBe(JSON.stringify(preservedBefore))
    expect(JSON.stringify(preservedAfter)).toContain('Escalation path')

    // …and the new vocabulary really did land in the same document, or this
    // proves only that an unedited document is unedited.
    const tag = findNode(afterEdit.doc, 'inlineTag')
    expect(tag, 'the inline tag must be stored alongside the preserved node').not.toBeNull()
    expect(attrOf(tag, 'label')).toBe(label)

    const link = findNode(afterEdit.doc, 'link')
    expect(link, 'the wikilink must be stored alongside the preserved node').not.toBeNull()
    expect(attrOf(link, 'target_title')).toBe('Incident review')

    expect(JSON.stringify(afterEdit.doc)).toContain('Filed under')
    expect(afterEdit.version).toBeGreaterThan(afterConversion.version)

    // The tag reached the page's tags too — the preserved block is not a wall
    // that stops the publish-time aggregation from reading the rest of the
    // document.
    expect((await storedTags(page, ctx, pageId)).map((t) => t.slug)).toContain(label)

    // And a reader gets all three, on the published page.
    const article = page.locator('article')
    await expect(article.getByTestId('codex-preserved-block')).toBeVisible({ timeout: 10000 })
    await expect(article.getByTestId('codex-inline-tag')).toHaveAttribute('data-label', label)
    await expect(article.locator('a[data-unresolved]')).toHaveCount(1)
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

/** One attribute of a node or mark found by [findNode]. */
function attrOf(node: unknown, name: string): unknown {
  return (node as { attrs?: Record<string, unknown> } | null)?.attrs?.[name]
}
