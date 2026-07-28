import { test, expect, type Page } from '@playwright/test'
import { createUserAndLogin, createSpace, getAuthToken, getCurrentUser } from './helpers/setup'

// Attachments, end to end (T6). The surface had zero E2E coverage: upload,
// list, the inline/download split the server decides from the object's own
// bytes, and — the case a browser is the only honest test of — an attachment
// reached through a share.
//
// The share-recipient journey is also the reproduction for S8. It is a real
// browser issuing the request the page's markup asks for, which is exactly the
// thing a request made with `page.request` (which carries whatever headers the
// test hands it) cannot stand in for.

/** A minimal PNG: signature + IHDR for a 1x1 image. Enough for
 *  http.DetectContentType, and enough for a browser to decode. */
const PNG_1X1 = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
  'base64',
)

/** A plain-text file: not on the inline allow-list, so the server streams it
 *  as application/octet-stream with Content-Disposition: attachment. */
const TEXT_FILE = Buffer.from('plain text, not an image\n')

async function createPage(page: Page, orgId: string, spaceId: string, title: string): Promise<string> {
  const token = await getAuthToken(page)
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/wiki`, {
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    data: { title, content: `# ${title}\n\nBody.` },
  })
  if (res.status() !== 201) throw new Error(`createPage: ${res.status()} ${await res.text()}`)
  return ((await res.json()) as { id: string }).id
}

async function uploadAttachment(
  page: Page,
  orgId: string,
  spaceId: string,
  pageId: string,
  name: string,
  mimeType: string,
  buffer: Buffer,
): Promise<{ id: string; filename: string; content_type: string }> {
  const token = await getAuthToken(page)
  const res = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/attachments`, {
    headers: { Authorization: `Bearer ${token}` },
    multipart: {
      entity_type: 'page',
      entity_id: pageId,
      file: { name, mimeType, buffer },
    },
  })
  if (res.status() !== 201) throw new Error(`upload ${name}: ${res.status()} ${await res.text()}`)
  return (await res.json()) as { id: string; filename: string; content_type: string }
}

test.describe('Attachments', () => {
  test('upload, list, and the inline/download split the server decides from the bytes', async ({ page }) => {
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)
    const spaceId = await createSpace(page, 'Attach Codex', 'codex')
    const run = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    const pageId = await createPage(page, orgId, spaceId, `Attach Page ${run}`)
    const token = await getAuthToken(page)

    const image = await uploadAttachment(page, orgId, spaceId, pageId, 'shot.png', 'image/png', PNG_1X1)
    const doc = await uploadAttachment(page, orgId, spaceId, pageId, 'notes.txt', 'text/plain', TEXT_FILE)

    // List: both are on the entity.
    const list = await page.request.get(
      `/api/v1/orgs/${orgId}/spaces/${spaceId}/attachments?entity_type=page&entity_id=${pageId}`,
      { headers: { Authorization: `Bearer ${token}` } },
    )
    expect(list.status()).toBe(200)
    const listed = (await list.json()) as Array<{ id: string; filename: string }>
    expect(listed.map((a) => a.filename).sort()).toEqual(['notes.txt', 'shot.png'])

    // The image streams inline, as the type sniffed from its own bytes.
    const img = await page.request.get(
      `/api/v1/orgs/${orgId}/spaces/${spaceId}/attachments/${image.id}`,
      { headers: { Authorization: `Bearer ${token}` } },
    )
    expect(img.status()).toBe(200)
    expect(img.headers()['content-type']).toContain('image/png')
    expect(img.headers()['content-disposition'] ?? '').toContain('inline')
    expect(Buffer.from(await img.body())).toEqual(PNG_1X1)

    // The non-image downloads, opaquely — never as a document the browser
    // would render on this origin.
    const txt = await page.request.get(
      `/api/v1/orgs/${orgId}/spaces/${spaceId}/attachments/${doc.id}`,
      { headers: { Authorization: `Bearer ${token}` } },
    )
    expect(txt.status()).toBe(200)
    expect(txt.headers()['content-type']).toContain('application/octet-stream')
    expect(txt.headers()['content-disposition'] ?? '').toContain('attachment')
    expect(Buffer.from(await txt.body())).toEqual(TEXT_FILE)
  })

  // S8. A share recipient opens the shared page in a browser, and the images
  // on it have to actually appear.
  //
  // This is the assertion the surface never had. Every other test of this path
  // issues the request itself and attaches a token to it; the browser does not.
  // The page's markup pointed <img src> straight at the attachment route, which
  // authenticates from an Authorization header or a session cookie — and this
  // frontend holds its bearer token in localStorage and sets no cookie, because
  // nothing in internal/core/api/auth ever calls http.SetCookie. So the browser
  // sent no credential, the route answered 401, and the reader saw a broken
  // image icon with nothing anywhere reporting a failure.
  //
  // naturalWidth is the assertion, not visibility: a broken <img> is still a
  // visible element with a non-zero box. Only a decoded image has a natural
  // width.
  test('a share recipient sees the images on a shared page, in a browser', async ({ page }) => {
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)
    const spaceId = await createSpace(page, 'Shared Attach Codex', 'codex')
    const run = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    const pageId = await createPage(page, orgId, spaceId, `Shared Attach ${run}`)
    const token = await getAuthToken(page)

    await uploadAttachment(page, orgId, spaceId, pageId, 'diagram.png', 'image/png', PNG_1X1)
    await uploadAttachment(page, orgId, spaceId, pageId, 'handout.txt', 'text/plain', TEXT_FILE)

    const shared = await page.request.post(`/api/v1/orgs/${orgId}/shares`, {
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      data: { entity_type: 'page', entity_id: pageId, audience: 'org' },
    })
    expect(shared.status()).toBe(201)

    await page.goto(`/shared/page/${pageId}`)
    await expect(page.getByTestId('shared-entity')).toBeVisible({ timeout: 15000 })
    await expect(page.getByTestId('shared-attachments')).toBeVisible()

    const image = page.getByTestId('shared-attachments').locator('img').first()
    await expect(image).toBeVisible()
    await expect
      .poll(async () => image.evaluate((el: HTMLImageElement) => el.naturalWidth), {
        timeout: 10000,
        message: 'the shared image never decoded — the browser could not load its bytes',
      })
      .toBeGreaterThan(0)

    // And every attachment, image or not, is reachable from this page — the
    // download list is deliberately not the complement of the preview filter.
    const links = page.getByTestId('shared-attachment-links').locator('a')
    await expect(links).toHaveCount(2)
    await expect(links.filter({ hasText: 'handout.txt' })).toHaveCount(1)
    await expect(links.filter({ hasText: 'diagram.png' })).toHaveCount(1)
  })

  // The other half of S8: the download links have to hand over actual bytes,
  // not a 401 body the browser saves as a file called "handout.txt".
  test('a share recipient can download a non-image attachment', async ({ page }) => {
    await createUserAndLogin(page)
    const { orgId } = await getCurrentUser(page)
    const spaceId = await createSpace(page, 'Shared Download Codex', 'codex')
    const run = `${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    const pageId = await createPage(page, orgId, spaceId, `Shared Download ${run}`)
    const token = await getAuthToken(page)

    await uploadAttachment(page, orgId, spaceId, pageId, 'handout.txt', 'text/plain', TEXT_FILE)
    const shared = await page.request.post(`/api/v1/orgs/${orgId}/shares`, {
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      data: { entity_type: 'page', entity_id: pageId, audience: 'org' },
    })
    expect(shared.status()).toBe(201)

    await page.goto(`/shared/page/${pageId}`)
    await expect(page.getByTestId('shared-entity')).toBeVisible({ timeout: 15000 })

    const download = page.waitForEvent('download')
    await page.getByTestId('shared-attachment-links').locator('a', { hasText: 'handout.txt' }).click()
    const file = await download
    expect(file.suggestedFilename()).toBe('handout.txt')
    const stream = await file.createReadStream()
    const chunks: Buffer[] = []
    for await (const chunk of stream) chunks.push(chunk as Buffer)
    expect(Buffer.concat(chunks)).toEqual(TEXT_FILE)
  })
})
