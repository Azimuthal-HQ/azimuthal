import { test, expect, type Browser, type Page } from '@playwright/test'
import {
  createUserAndLogin,
  getAuthToken,
  getCurrentUser,
  getPortalToken,
} from './helpers/setup'

// The customer portal (PR #85, migrations 044/045) — the journey the surface
// exists for, and the three boundaries it is only safe if it holds.
//
// WHY THIS CANNOT BE A GO TEST OR A VITEST.
//
// Every other portal test in this repository owns one half of the system. The
// Go integration tests drive the API with tokens they mint themselves; the
// vitest suite renders components against fixtures it writes itself. Both halves
// pass today. What neither can reach is the SEAM, and the seam is where this
// feature's failures live:
//
//  1. THE MAGIC LINK IS A URL, NOT A TOKEN. The server composes
//     `{APP_BASE_URL}/portal/{portalKey}/signin/{rawToken}` as a PATH segment
//     (internal/core/portal/service.go), no server route matches it, the SPA
//     handler answers index.html, and only web/src/App.tsx's route declaration
//     gives the string meaning. A Go test asserts the token redeems; a vitest
//     asserts the component redeems a token handed to it. Neither notices if the
//     route shape and the emitted URL stop agreeing, which silently breaks every
//     link already sitting in a customer's inbox.
//
//  2. THE CREDENTIAL BOUNDARY IS TESTED AGAINST THE TOKEN THE BROWSER STORED.
//     Portal and internal sessions are signed with the SAME RSA key and
//     separated only by the `aud` claim (internal/core/portal/token.go). A Go
//     test can mint a portal token and prove the internal parser refuses it. It
//     cannot prove the thing the redeem page actually persisted — after the
//     round trip, the JSON envelope and localStorage — is that same refused
//     thing. `getPortalToken` reads what the UI stored, which is the half only a
//     browser has.
//
//  3. ZERO CONTEXT IS A PROPERTY OF THE RENDERED DOCUMENT. The vitest sweep
//     (web/src/pages/portal/__tests__/portalZeroContext.test.tsx) proves the
//     components do not render container identity from ENRICHED FIXTURES it
//     invents. Here the space name, key, slug and id are REAL — they exist in
//     this database, the ticket really lives in that space, and the assertion is
//     against the document a real customer's browser really built.
//
// WHICH MUTATIONS THESE SURVIVE. Each of the following breaks at least one
// assertion below, and none of them breaks a Go test or a vitest:
//
//  - deleting `AZIMUTHAL_PORTAL_LINK_DELIVERY`/`APP_BASE_URL` from the E2E
//    webServer env (the disclosed URL becomes empty, or points at :8080)
//  - changing the redeem route from a path segment to `?token=`
//  - dropping `RequirePortalSession` from the router (a signed-out customer
//    reaches the requests list)
//  - deleting the `visibility` field from the comment composer's create call
//    (the note defaults internal server-side, so the AGENT-side assertions stay
//    green while step 9's public reply never reaches the customer)
//  - parameterising `c.visibility = 'public'` in ListPortalTicketComments
//  - dropping `requester_id` from GetPortalRequest's WHERE (requester B reads
//    requester A's request)
//  - removing the audience ParserOption from either token family
//  - reaching for the richer ticket DTO on any portal response
//
// A NOTE ON THE ORDERING OF SIGN-IN, because it is a trap rather than a
// preference: `CreateMagicLink` runs `InvalidateOutstandingLinks` in the SAME
// transaction before inserting, so issuing a link kills every outstanding
// unconsumed link for that (requester, portal) pair. Driving the UI form and
// then redeeming a token captured earlier redeems a DEAD link, and the server
// answers a perfectly truthful 401 that reads exactly like a real defect. The
// order is encoded once, in `signInThroughThePortal`, and explained there.

// ---------------------------------------------------------------------------
// Seeding
// ---------------------------------------------------------------------------

/** A token unique to one test, embedded in everything that test seeds. */
function runToken(): string {
  return `p${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
}

async function jsonHeaders(page: Page): Promise<Record<string, string>> {
  return {
    Authorization: `Bearer ${await getAuthToken(page)}`,
    'Content-Type': 'application/json',
  }
}

/**
 * Everything the agent side knows about the desk, and everything the customer
 * side must never learn about it.
 *
 * `forbidden` is not a list of plausible-looking strings: every value in it is
 * a REAL identifier of the container this portal is attached to, created in
 * this run, and the ticket the customer raises really does live behind them.
 */
interface Desk {
  orgId: string
  spaceId: string
  portalKey: string
  portalName: string
  forbidden: Record<string, string>
}

/**
 * Creates a Beacon space, opts it into the portal, and READS BOTH BACK.
 *
 * The read-backs are not ceremony. Portal creation answers 400 on any space
 * that is not a Beacon service desk (`portal.Service.CreatePortal`), so a space
 * whose `type` silently defaulted would fail the create with a message about
 * service desks and send a reader hunting in the portal code. And the whole
 * zero-context sweep is asserted against the space's name, key and slug — if
 * the create had silently normalised any of them, the sweep would still pass
 * while checking for strings that were never real.
 */
async function createDesk(page: Page, orgId: string): Promise<Desk> {
  // A token of its own, deliberately NOT shared with anything the customer
  // types. The sweep checks for these strings in the customer's document; if
  // the summary a customer types contained the same token, an accidental hit
  // would read as a container leak.
  const stamp = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
  const spaceName = `Escalations Desk ${stamp}`
  const spaceSlug = `escalations-desk-${stamp}`
  const spaceKey = ('PT' + Math.random().toString(36).replace(/[^a-z0-9]/g, '').toUpperCase()).slice(0, 8)

  const headers = await jsonHeaders(page)
  const created = await page.request.post(`/api/v1/orgs/${orgId}/spaces`, {
    headers,
    data: { name: spaceName, slug: spaceSlug, key: spaceKey, type: 'beacon' },
  })
  if (created.status() !== 201) {
    throw new Error(`create beacon space: ${created.status()} ${await created.text()}`)
  }
  const spaceId = ((await created.json()) as { id: string }).id

  const check = await page.request.get(`/api/v1/orgs/${orgId}/spaces/${spaceId}`, { headers })
  if (check.status() !== 200) throw new Error(`read back space: ${check.status()} ${await check.text()}`)
  const space = (await check.json()) as { type?: string; key?: string; slug?: string; name?: string }
  if (space.type !== 'beacon') {
    throw new Error(`space seeded as type ${space.type}, wanted beacon — a portal cannot attach to it`)
  }
  if (space.key !== spaceKey || space.slug !== spaceSlug || space.name !== spaceName) {
    throw new Error(
      `space identity was normalised on create (${space.name}/${space.key}/${space.slug}); ` +
        'the zero-context sweep would be checking for strings that do not exist',
    )
  }

  // The portal's public name is deliberately UNRELATED to the space's. It is
  // the one string the customer is shown, so naming it after the container
  // would make the sweep below pass while the container name sat on the page.
  const portalName = 'Aurora Support'
  const portalRes = await page.request.post(`/api/v1/orgs/${orgId}/spaces/${spaceId}/portal`, {
    headers,
    data: { name: portalName, intro: 'Tell us what you need and we will pick it up.' },
  })
  if (portalRes.status() !== 201) {
    throw new Error(`create portal: ${portalRes.status()} ${await portalRes.text()}`)
  }
  const portal = (await portalRes.json()) as { portal_key: string; enabled: boolean; name: string }

  const readBack = await page.request.get(`/api/v1/orgs/${orgId}/spaces/${spaceId}/portal`, { headers })
  if (readBack.status() !== 200) throw new Error(`read back portal: ${readBack.status()}`)
  const stored = (await readBack.json()) as { portal_key: string; enabled: boolean; name: string }
  expect(stored.portal_key, 'the stored portal key must be the one the create returned').toBe(
    portal.portal_key,
  )
  expect(stored.enabled, 'a freshly created portal must be enabled, or every customer step 404s').toBe(
    true,
  )
  expect(stored.name).toBe(portalName)

  return {
    orgId,
    spaceId,
    portalKey: portal.portal_key,
    portalName,
    forbidden: {
      'space name': spaceName,
      'space key': spaceKey,
      'space slug': spaceSlug,
      'space id': spaceId,
      'organisation id': orgId,
      'module route': '/beacon/',
    },
  }
}

/**
 * Creates a Beacon space WITHOUT a portal, for the journey that creates one
 * through the settings UI (A1). The type read-back mirrors createDesk's: a
 * portal cannot attach to a space whose `type` silently defaulted, and the
 * failure would otherwise surface as a 400 deep inside the create click.
 */
async function createBeaconSpace(page: Page, orgId: string): Promise<{ spaceId: string }> {
  const stamp = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
  const headers = await jsonHeaders(page)
  const created = await page.request.post(`/api/v1/orgs/${orgId}/spaces`, {
    headers,
    data: {
      name: `Portal Desk ${stamp}`,
      slug: `portal-desk-${stamp}`,
      key: ('PD' + Math.random().toString(36).replace(/[^a-z0-9]/g, '').toUpperCase()).slice(0, 8),
      type: 'beacon',
    },
  })
  if (created.status() !== 201) {
    throw new Error(`create beacon space: ${created.status()} ${await created.text()}`)
  }
  const spaceId = ((await created.json()) as { id: string }).id

  const check = await page.request.get(`/api/v1/orgs/${orgId}/spaces/${spaceId}`, { headers })
  if (check.status() !== 200) throw new Error(`read back space: ${check.status()}`)
  const space = (await check.json()) as { type?: string }
  if (space.type !== 'beacon') {
    throw new Error(`space seeded as type ${space.type}, wanted beacon — a portal cannot attach to it`)
  }
  return { spaceId }
}

// ---------------------------------------------------------------------------
// The requester's side
// ---------------------------------------------------------------------------

/**
 * Issues a sign-in link through the public endpoint and returns its URL.
 *
 * The endpoint answers 202 for EVERY address — known, unknown or deactivated
 * (`portal.Service.RequestLink`) — so the status code is asserted as 202 and
 * never used as evidence that an address exists. There is no negative form of
 * this assertion to write.
 */
async function requestLink(page: Page, portalKey: string, email: string, name?: string): Promise<string> {
  const res = await page.request.post(`/api/v1/portal/${portalKey}/auth/request-link`, {
    headers: { 'Content-Type': 'application/json' },
    data: name ? { email, name } : { email },
  })
  expect(res.status(), 'request-link answers 202 for every address, known or not').toBe(202)
  const body = (await res.json()) as { magic_link_url?: string }
  const url = body.magic_link_url ?? ''
  // A premise, not decoration. With disclosure off there is no way for a
  // browser to sign a requester in, and every assertion after this point would
  // fail somewhere far less legible.
  //
  // The message names both settings on purpose. Disclosure requires
  // AZIMUTHAL_PORTAL_DISCLOSE_LINK=true AND a non-production APP_ENV, and the
  // likeliest cause of seeing this locally is not a config bug at all:
  // playwright.config.ts sets `reuseExistingServer: !process.env.CI`, so a
  // server somebody else already started on this port is adopted as-is and
  // never receives webServer.env. APP_ENV now defaults to production, which
  // makes a stray `azimuthal serve` exactly the server that fails here.
  expect(
    url,
    'the portal suite needs AZIMUTHAL_PORTAL_DISCLOSE_LINK=true and a non-production ' +
      'APP_ENV, both set in playwright.config.ts webServer.env — if they are set, ' +
      'suspect a reused server on this port that never saw them',
  ).toBeTruthy()
  return url
}

/** The token out of a magic-link URL, read structurally rather than by regex. */
function tokenOf(magicLinkUrl: string): string {
  return new URL(magicLinkUrl).pathname.split('/').pop() ?? ''
}

/**
 * Signs a requester in the way a real customer does, and lands them on their
 * requests.
 *
 * THE ORDER OF THE TWO LINKS IS LOAD-BEARING. `CreateMagicLink` calls
 * `InvalidateOutstandingLinks` in the same transaction before inserting, so
 * issuing a link supersedes every outstanding one for that (requester, portal)
 * pair — "request another link" must never leave two live credentials in one
 * inbox. So the UI form goes FIRST (it issues link A and proves the form does
 * what it claims), and the link that is redeemed is the LATER one, B. Reversed,
 * the captured token is already dead and redeem answers a truthful 401 that
 * reads exactly like a defect in the redeem page.
 *
 * The navigation is by PATHNAME, so it resolves against `use.baseURL` and is
 * port-correct by construction — the invite idiom from admin.spec.ts. The
 * emitted absolute URL is separately correct (playwright.config.ts sets
 * APP_BASE_URL to the E2E port), and depending on it here would make this
 * suite fail on a port change rather than on a behaviour change.
 */
async function signInThroughThePortal(
  page: Page,
  portalKey: string,
  email: string,
  name: string,
): Promise<void> {
  await page.goto(`/portal/${portalKey}`)
  await expect(page.getByTestId('portal-signin-page')).toBeVisible({ timeout: 15000 })
  await page.getByTestId('portal-email').fill(email)
  await page.getByTestId('portal-name-input').fill(name)
  await page.getByTestId('portal-signin-submit').click()

  // The terminal state is conditional by design ("If … can raise requests
  // here"), because copy that confirmed the address would undo server-side the
  // enumeration decision the 202 was protecting.
  await expect(page.getByTestId('portal-link-sent')).toBeVisible({ timeout: 15000 })

  const magicLinkUrl = await requestLink(page, portalKey, email, name)
  await page.goto(new URL(magicLinkUrl).pathname)
  await expect(page.getByTestId('portal-requests-page')).toBeVisible({ timeout: 15000 })
}

/** Raises a request through the compose form and returns its opaque reference. */
async function raiseRequest(
  page: Page,
  portalKey: string,
  summary: string,
  description: string,
): Promise<string> {
  await page.getByTestId('portal-new-request').click()
  await expect(page.getByTestId('portal-new-request-page')).toBeVisible({ timeout: 15000 })
  await page.getByTestId('portal-summary').fill(summary)
  await page.getByTestId('portal-description').fill(description)
  await page.getByTestId('portal-new-request-submit').click()

  await expect(page).toHaveURL(new RegExp(`/portal/${portalKey}/requests/[0-9a-f-]{36}$`), {
    timeout: 15000,
  })
  await expect(page.getByTestId('portal-request-detail-page')).toBeVisible()
  return new URL(page.url()).pathname.split('/').pop() ?? ''
}

// ---------------------------------------------------------------------------
// The zero-context sweep
// ---------------------------------------------------------------------------

/**
 * Asserts that no real identifier of the container reached this document.
 *
 * MARKUP, not text: `title`, `aria-label`, `data-*` and `href` are all places a
 * space id can hide where `textContent` would never see it, and an href is the
 * easiest leak of all — `/beacon/{spaceId}/tickets/{id}` names the module, the
 * space and the internal ticket in one string.
 *
 * Case-insensitive, which is strictly stronger: the space key is stored
 * uppercase and the slug lowercase, and a component that lower-cased a heading
 * would otherwise slip through.
 *
 * EVERY CALLER MAKES A POSITIVE SIGHTING FIRST. Without one, a page that
 * rendered nothing — a crashed boundary, a guard that redirected, a renamed
 * component — passes every "does not contain" assertion and reads as proof of a
 * guarantee it never tested.
 */
async function assertNoContainerContext(page: Page, desk: Desk, where: string): Promise<void> {
  const markup = (await page.evaluate(() => document.body.innerHTML)).toLowerCase()
  for (const [label, value] of Object.entries(desk.forbidden)) {
    expect(markup, `${where} must not disclose the ${label} (${value})`).not.toContain(
      value.toLowerCase(),
    )
  }
}

// ---------------------------------------------------------------------------
// The journeys
// ---------------------------------------------------------------------------

test.describe('The customer portal', () => {
  test('a requester signs in by link, raises a request, and is told only what the team chose to tell them', async ({
    browser,
  }: {
    browser: Browser
  }) => {
    test.setTimeout(120_000)

    const agentCtx = await browser.newContext()
    const agent = await agentCtx.newPage()
    // The customer's context is never logged in and never will be. That is the
    // point: an external requester has no users row, no membership and no
    // grant, so a context that had ever held an internal token would prove
    // nothing about what a real customer can reach.
    const customerCtx = await browser.newContext()
    const customer = await customerCtx.newPage()

    const run = runToken()
    const customerEmail = `requester-${run}@example.com`
    const customerName = `Dana ${run}`
    const summary = `Cannot export the ledger ${run}`
    const description = `Every export finishes at ninety per cent and stops. Run ${run}.`
    const internalNote = `Internal triage note ${run} — check the worker queue`
    const publicReply = `Thanks for the detail — we have reproduced it. Ref ${run}.`
    const customerReply = `Understood, thank you. Anything else you need from me? ${run}`

    // ── Setup, agent side ────────────────────────────────────────────────
    await createUserAndLogin(agent)
    const { orgId } = await getCurrentUser(agent)
    const desk = await createDesk(agent, orgId)

    // ── The customer signs in ────────────────────────────────────────────
    await signInThroughThePortal(customer, desk.portalKey, customerEmail, customerName)
    await expect(customer.getByTestId('portal-name')).toHaveText(desk.portalName)

    // ── The customer raises a request ────────────────────────────────────
    const reference = await raiseRequest(customer, desk.portalKey, summary, description)

    // "Received" is the requester-facing rendering of the internal `open`
    // (`requesterStatus`). Asserting the internal word here would pass while
    // the customer was being shown process vocabulary.
    await expect(customer.getByTestId('portal-status')).toHaveText('Received')
    await customer.goto(`/portal/${desk.portalKey}/requests`)
    const row = customer.getByTestId('portal-request-row').filter({ hasText: summary })
    await expect(row).toHaveCount(1)
    await expect(row.getByTestId('portal-status')).toHaveText('Received')

    // ── The agent opens the same thing from the inside ───────────────────
    // The portal's `reference` IS the ticket id, used as an opaque handle, so
    // the agent's own URL is composable from it without a lookup.
    await agent.goto(`/beacon/${desk.spaceId}/tickets/${reference}`)
    await expect(agent.getByRole('heading', { name: summary })).toBeVisible({ timeout: 15000 })

    // The requester is a real identity, not a hole. Before this phase the
    // agent surface read `reporter_id` — NULL on a portal ticket by migration
    // 044's XOR — and rendered the literal "Unknown". The page relabels the
    // field rather than reusing it, so the assertion is that the REQUESTER
    // field carries the address AND that the reporter field is not rendered at
    // all; the second half is what fails if the relabelling is undone.
    const requesterField = agent.getByTestId('ticket-requester')
    await expect(requesterField).toBeVisible()
    await expect(requesterField).toContainText(customerEmail)
    await expect(requesterField).toContainText(customerName)
    await expect(requesterField).not.toContainText('Unknown')
    await expect(agent.getByTestId('ticket-reporter')).toHaveCount(0)

    // Provenance: this ticket came from outside.
    await expect(agent.getByTestId('portal-origin-chip')).toBeVisible()

    // ── The composer's audience, BEFORE anything is typed ────────────────
    // Internal is the safe default and the default is the assertion: a public
    // note that should have been internal is a disclosure that cannot be
    // recalled, and the reverse is a delay.
    await expect(agent.getByTestId('comment-visibility-state')).toHaveText(
      'Only your team can see this.',
    )
    await agent.getByTestId('comment-composer').fill(internalNote)
    await agent.getByRole('button', { name: 'Comment', exact: true }).click()
    const internalRow = agent.getByTestId('comment-row').filter({ hasText: internalNote })
    await expect(internalRow).toHaveCount(1, { timeout: 15000 })
    await expect(internalRow).toHaveAttribute('data-visibility', 'internal')
    await expect(internalRow.getByTestId('comment-public-marker')).toHaveCount(0)

    // ── The customer must not see it ─────────────────────────────────────
    // The absence is paired with two positive sightings in the same block. On
    // a page that failed to render, "the note is absent" is true and means
    // nothing.
    await customer.goto(`/portal/${desk.portalKey}/requests/${reference}`)
    await expect(customer.getByTestId('portal-request-detail-page')).toBeVisible({ timeout: 15000 })
    await expect(customer.getByRole('heading', { name: summary })).toBeVisible()
    await expect(customer.getByTestId('portal-request-description')).toContainText(description)
    await expect(customer.getByText(internalNote)).toHaveCount(0)
    await expect(customer.getByTestId('portal-message')).toHaveCount(0)

    // ── The agent replies publicly ───────────────────────────────────────
    await agent.getByTestId('comment-visibility').locator('[data-value="public"]').click()
    await expect(agent.getByTestId('comment-visibility-state')).toHaveText(
      'The customer will see this.',
    )
    await agent.getByTestId('comment-composer').fill(publicReply)
    await agent.getByRole('button', { name: 'Comment', exact: true }).click()
    const publicRow = agent.getByTestId('comment-row').filter({ hasText: publicReply })
    await expect(publicRow).toHaveCount(1, { timeout: 15000 })
    await expect(publicRow).toHaveAttribute('data-visibility', 'public')
    await expect(publicRow.getByTestId('comment-public-marker')).toBeVisible()
    // The toggle does not stick: one deliberate customer reply must not become
    // the default for the next note.
    await expect(agent.getByTestId('comment-visibility-state')).toHaveText(
      'Only your team can see this.',
    )

    // ── The customer sees the public one, and still not the internal one ──
    await customer.reload()
    await expect(customer.getByTestId('portal-message')).toHaveCount(1, { timeout: 15000 })
    await expect(customer.getByTestId('portal-message')).toContainText(publicReply)
    await expect(customer.getByTestId('portal-message')).toHaveAttribute(
      'data-from-requester',
      'false',
    )
    await expect(customer.getByText(internalNote)).toHaveCount(0)

    // ── The customer replies, and the thread closes the loop ─────────────
    await customer.getByTestId('portal-reply-body').fill(customerReply)
    await customer.getByTestId('portal-reply-submit').click()
    const mine = customer.getByTestId('portal-message').filter({ hasText: customerReply })
    await expect(mine).toHaveCount(1, { timeout: 15000 })
    await expect(mine).toHaveAttribute('data-from-requester', 'true')

    await agent.reload()
    const customerRow = agent.getByTestId('comment-row').filter({ hasText: customerReply })
    await expect(customerRow).toHaveCount(1, { timeout: 15000 })
    // The requester treatment — `from_requester` rendered, not merely carried.
    await expect(customerRow.getByTestId('comment-requester-chip')).toBeVisible()
    // A requester's own message is public by database constraint (migration
    // 045's comments_requester_public), and the agent surface shows it as such.
    await expect(customerRow).toHaveAttribute('data-visibility', 'public')

    await agentCtx.close()
    await customerCtx.close()
  })

  test('a superseded sign-in link fails closed, storing nothing and unlocking nothing', async ({
    browser,
  }: {
    browser: Browser
  }) => {
    test.setTimeout(120_000)

    const agentCtx = await browser.newContext()
    const agent = await agentCtx.newPage()
    const customerCtx = await browser.newContext()
    const customer = await customerCtx.newPage()

    const run = runToken()
    await createUserAndLogin(agent)
    const { orgId } = await getCurrentUser(agent)
    const desk = await createDesk(agent, orgId)

    const email = `rotated-${run}@example.com`
    const first = await requestLink(customer, desk.portalKey, email, `Rotated ${run}`)
    const second = await requestLink(customer, desk.portalKey, email, `Rotated ${run}`)
    // The premise: two DIFFERENT links were issued. Without this the test would
    // pass trivially against a server that returned the same token twice.
    expect(tokenOf(first), 'each request must issue a distinct link').not.toBe(tokenOf(second))

    // The FIRST one is now superseded — `InvalidateOutstandingLinks` ran inside
    // `CreateMagicLink`'s transaction when the second was issued.
    await customer.goto(new URL(first).pathname)
    await expect(customer.getByTestId('portal-redeem-failed')).toBeVisible({ timeout: 15000 })
    // The recovery goes to the portal's own sign-in page, never to /login: an
    // external requester has no internal account and cannot make one.
    await expect(customer.getByTestId('portal-redeem-retry')).toBeVisible()
    expect(customer.url(), 'a refused link must not bounce a customer to the internal login').not.toContain(
      '/login',
    )

    // Nothing was stored. `setPortalSession` is called from `onSuccess` and
    // nowhere else; an optimistic write here would leave a "session" the server
    // has already refused, and every later request would 401 into a redirect
    // loop that looks like an outage.
    const stored = await customer.evaluate(() =>
      Object.keys(localStorage).filter((k) => k.startsWith('azimuthal_portal_session:')),
    )
    expect(stored, 'a refused link must store no portal session').toEqual([])

    // And the guard still holds: the requests list bounces back to sign-in.
    await customer.goto(`/portal/${desk.portalKey}/requests`)
    await expect(customer.getByTestId('portal-signin-page')).toBeVisible({ timeout: 15000 })
    await expect(customer.getByTestId('portal-requests-page')).toHaveCount(0)

    // The positive half, and the reason the assertions above are not vacuous:
    // the SECOND link still works, so "fails closed" is about supersession and
    // not about a portal that was broken for everyone.
    await customer.goto(new URL(second).pathname)
    await expect(customer.getByTestId('portal-requests-page')).toBeVisible({ timeout: 15000 })

    await agentCtx.close()
    await customerCtx.close()
  })

  test('one requester never learns of another requester’s request, in either direction', async ({
    browser,
  }: {
    browser: Browser
  }) => {
    test.setTimeout(120_000)

    const agentCtx = await browser.newContext()
    const agent = await agentCtx.newPage()
    const aCtx = await browser.newContext()
    const a = await aCtx.newPage()
    const bCtx = await browser.newContext()
    const b = await bCtx.newPage()

    const run = runToken()
    await createUserAndLogin(agent)
    const { orgId } = await getCurrentUser(agent)
    const desk = await createDesk(agent, orgId)

    const aSummary = `Alpha billing question ${run}`
    const bSummary = `Bravo password question ${run}`

    await signInThroughThePortal(a, desk.portalKey, `alpha-${run}@example.com`, `Alpha ${run}`)
    const aRef = await raiseRequest(a, desk.portalKey, aSummary, `Alpha detail ${run}`)

    await signInThroughThePortal(b, desk.portalKey, `bravo-${run}@example.com`, `Bravo ${run}`)
    const bRef = await raiseRequest(b, desk.portalKey, bSummary, `Bravo detail ${run}`)
    expect(aRef, 'the two requesters must have raised two different requests').not.toBe(bRef)

    // B reaching for A's reference gets the same answer as for a reference that
    // does not exist — `GetPortalRequest` scopes by requester_id in the query,
    // so the row is never in the result set and there is no 403 to confirm it
    // exists (§2.6).
    await b.goto(`/portal/${desk.portalKey}/requests/${aRef}`)
    await expect(b.getByTestId('portal-detail-retry')).toBeVisible({ timeout: 15000 })
    await expect(b.getByText(aSummary)).toHaveCount(0)
    // And A reaching for B's, so this is a property of the scoping and not an
    // accident of which request was raised first.
    await a.goto(`/portal/${desk.portalKey}/requests/${bRef}`)
    await expect(a.getByTestId('portal-detail-retry')).toBeVisible({ timeout: 15000 })
    await expect(a.getByText(bSummary)).toHaveCount(0)

    // Each list holds exactly its owner's request. The positive sighting is
    // what stops the negative from being vacuous — a list that failed to load
    // contains neither summary.
    await b.goto(`/portal/${desk.portalKey}/requests`)
    await expect(b.getByTestId('portal-request-row').filter({ hasText: bSummary })).toHaveCount(1)
    await expect(b.getByTestId('portal-request-row').filter({ hasText: aSummary })).toHaveCount(0)

    await a.goto(`/portal/${desk.portalKey}/requests`)
    await expect(a.getByTestId('portal-request-row').filter({ hasText: aSummary })).toHaveCount(1)
    await expect(a.getByTestId('portal-request-row').filter({ hasText: bSummary })).toHaveCount(0)

    await agentCtx.close()
    await aCtx.close()
    await bCtx.close()
  })

  test('a portal session is refused by the internal API, and an internal session by the portal', async ({
    browser,
  }: {
    browser: Browser
  }) => {
    test.setTimeout(120_000)

    const agentCtx = await browser.newContext()
    const agent = await agentCtx.newPage()
    const customerCtx = await browser.newContext()
    const customer = await customerCtx.newPage()

    const run = runToken()
    await createUserAndLogin(agent)
    const { orgId } = await getCurrentUser(agent)
    const desk = await createDesk(agent, orgId)
    await signInThroughThePortal(customer, desk.portalKey, `both-${run}@example.com`, `Both ${run}`)

    // The token the UI ACTUALLY STORED, not one minted for the occasion. Both
    // families are signed with the same RSA key and separated only by `aud`, so
    // the interesting question is whether the thing in this browser's
    // localStorage is refused — and that is the half a Go test cannot reach.
    const portalToken = await getPortalToken(customer, desk.portalKey)
    const internalToken = await getAuthToken(agent)
    expect(portalToken, 'the two credentials must not be the same string').not.toBe(internalToken)

    const asPortal = await customer.request.get('/api/v1/auth/me', {
      headers: { Authorization: `Bearer ${portalToken}` },
    })
    expect(asPortal.status(), 'a portal session must never pass internal RequireAuth').toBe(401)

    const asInternal = await agent.request.get(`/api/v1/portal/${desk.portalKey}/my/requests`, {
      headers: { Authorization: `Bearer ${internalToken}` },
    })
    expect(asInternal.status(), 'an internal session must never pass the portal guard').toBe(401)

    // Both positives, so the two 401s above are about the audience boundary and
    // not about two tokens that had simply stopped working.
    const portalOk = await customer.request.get(`/api/v1/portal/${desk.portalKey}/my/requests`, {
      headers: { Authorization: `Bearer ${portalToken}` },
    })
    expect(portalOk.status(), 'the portal token must still work on its own surface').toBe(200)
    const internalOk = await agent.request.get('/api/v1/auth/me', {
      headers: { Authorization: `Bearer ${internalToken}` },
    })
    expect(internalOk.status(), 'the internal token must still work on its own surface').toBe(200)

    await agentCtx.close()
    await customerCtx.close()
  })

  test('no portal page discloses the space, the organisation or the module behind it', async ({
    browser,
  }: {
    browser: Browser
  }) => {
    test.setTimeout(120_000)

    const agentCtx = await browser.newContext()
    const agent = await agentCtx.newPage()
    const customerCtx = await browser.newContext()
    const customer = await customerCtx.newPage()

    const run = runToken()
    await createUserAndLogin(agent)
    const { orgId } = await getCurrentUser(agent)
    const desk = await createDesk(agent, orgId)

    const summary = `Sweep request ${run}`
    const description = `Sweep detail ${run}`

    // 1. The anonymous sign-in page — the only page a stranger can reach.
    await customer.goto(`/portal/${desk.portalKey}`)
    await expect(customer.getByTestId('portal-signin-page')).toBeVisible({ timeout: 15000 })
    await expect(customer.getByTestId('portal-name')).toHaveText(desk.portalName)
    await assertNoContainerContext(customer, desk, 'the portal sign-in page')

    await signInThroughThePortal(customer, desk.portalKey, `sweep-${run}@example.com`, `Sweep ${run}`)
    const reference = await raiseRequest(customer, desk.portalKey, summary, description)

    // 2. The requests list, with a row on it — an empty list would sweep clean
    //    while proving nothing about how a row renders.
    await customer.goto(`/portal/${desk.portalKey}/requests`)
    await expect(
      customer.getByTestId('portal-request-row').filter({ hasText: summary }),
    ).toHaveCount(1)
    await assertNoContainerContext(customer, desk, 'the requests list')

    // 3. The request detail, which is the richest page and the one whose row
    //    really does live in that space.
    await customer.goto(`/portal/${desk.portalKey}/requests/${reference}`)
    await expect(customer.getByRole('heading', { name: summary })).toBeVisible({ timeout: 15000 })
    await expect(customer.getByTestId('portal-request-description')).toContainText(description)
    await assertNoContainerContext(customer, desk, 'the request detail')

    // The agent's own page is the control: every forbidden string above is a
    // REAL identifier, and the internal surface is where they legitimately
    // appear. Without this the sweep could be passing because the values were
    // never rendered anywhere by anyone.
    await agent.goto(`/beacon/${desk.spaceId}/tickets/${reference}`)
    await expect(agent.getByRole('heading', { name: summary })).toBeVisible({ timeout: 15000 })
    const agentMarkup = (await agent.evaluate(() => document.body.innerHTML)).toLowerCase()
    expect(
      agentMarkup,
      'the agent surface must show the space id — otherwise the customer-side sweep proves nothing',
    ).toContain(desk.spaceId.toLowerCase())

    await agentCtx.close()
    await customerCtx.close()
  })

  test('the portal is created and run entirely from space settings, and the URL on the page is the customer door', async ({
    browser,
  }: {
    browser: Browser
  }) => {
    test.setTimeout(120_000)

    // A1. Until this phase, every test above seeded its portal by RAW REQUEST
    // because there was no UI path — the capability existed and was
    // undiscoverable. This journey is the proof that it now isn't: the portal
    // is created in space settings, the key is read off the page (never from
    // an API response), and the URL the page displays is what a customer
    // reaches the sign-in through. The rename and toggle legs drive the
    // widened PATCH through the browser: each travels alone on the wire, so a
    // rename that disabled the portal — the anti-clear defect the three-state
    // body exists to prevent — would fail the sign-in reload below.
    const agentCtx = await browser.newContext()
    const agent = await agentCtx.newPage()
    const customerCtx = await browser.newContext()
    const customer = await customerCtx.newPage()

    await createUserAndLogin(agent)
    const { orgId } = await getCurrentUser(agent)
    const { spaceId } = await createBeaconSpace(agent, orgId)

    // ── No portal yet: settings offers to create one, name required ──────
    await agent.goto(`/beacon/${spaceId}/settings`)
    await expect(agent.getByTestId('portal-section')).toBeVisible({ timeout: 15000 })
    await expect(agent.getByTestId('portal-create')).toBeVisible()
    await expect(
      agent.getByTestId('portal-create-button'),
      'the create button must hold until a name is typed',
    ).toBeDisabled()

    const portalName = 'Meridian Support'
    await agent.getByTestId('portal-create-name').fill(portalName)
    await agent.getByTestId('portal-create-intro').fill('Tell us what broke and we will chase it.')
    await agent.getByTestId('portal-create-button').click()

    // ── The key is read FROM THE PAGE — the discoverability claim itself ──
    await expect(agent.getByTestId('portal-configured')).toBeVisible({ timeout: 15000 })
    const portalKey = ((await agent.getByTestId('portal-config-key').textContent()) ?? '').trim()
    expect(portalKey, 'the page must show a real portal key').toMatch(/^[a-z0-9]{16,32}$/)

    // The displayed URL is the FULL customer URL for exactly that key, not a
    // bare key an agent would have to know what to do with.
    const shownUrl = ((await agent.getByTestId('portal-config-url').textContent()) ?? '').trim()
    expect(new URL(shownUrl).pathname, 'the shown URL must be the customer door for the shown key').toBe(
      `/portal/${portalKey}`,
    )

    // ── A customer who was handed that URL reaches the sign-in ───────────
    // Navigation by pathname, the invite idiom: port-correct by construction.
    await customer.goto(`/portal/${portalKey}`)
    await expect(customer.getByTestId('portal-signin-page')).toBeVisible({ timeout: 15000 })
    await expect(customer.getByTestId('portal-name')).toHaveText(portalName)

    // ── Rename in settings: the public face follows, the key does not ────
    await agent.getByTestId('portal-config-name').fill('Meridian Helpdesk')
    await agent.getByTestId('portal-config-save').click()
    await expect(
      agent.getByTestId('portal-config-save'),
      'a completed save returns the form to clean',
    ).toBeDisabled({ timeout: 15000 })
    await expect(agent.getByTestId('portal-config-key')).toHaveText(portalKey)

    await customer.reload()
    await expect(customer.getByTestId('portal-name')).toHaveText('Meridian Helpdesk')

    // ── Disable: the same URL goes dark for customers, not for settings ──
    // A disabled portal renders PortalLayout's unavailable state — the frame
    // answers once for every child page, and its copy deliberately does not
    // distinguish "no such portal" from "switched off" (the server refuses
    // to distinguish them too).
    await agent.getByTestId('portal-config-toggle').click()
    await expect(agent.getByTestId('portal-config-state')).toHaveText('The portal is disabled', {
      timeout: 15000,
    })
    await customer.reload()
    await expect(customer.getByText(/This portal isn.t available/)).toBeVisible({ timeout: 15000 })
    await expect(customer.getByTestId('portal-signin-page')).toHaveCount(0)

    // ── Re-enable: the SAME URL comes back — the key survived the round trip
    await agent.getByTestId('portal-config-toggle').click()
    await expect(agent.getByTestId('portal-config-state')).toHaveText('The portal is live', {
      timeout: 15000,
    })
    await customer.reload()
    await expect(customer.getByTestId('portal-signin-page')).toBeVisible({ timeout: 15000 })
    await expect(customer.getByTestId('portal-name')).toHaveText('Meridian Helpdesk')

    await agentCtx.close()
    await customerCtx.close()
  })
})
