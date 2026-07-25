import { test, expect, type Page } from '@playwright/test'
import { createUserAndLogin, createSpace, assertNoErrors, getAuthToken, getCurrentUser } from './helpers/setup'

// W4: board customization — configured columns, soft WIP limits, swimlanes,
// and the type filter composing with both.

interface Ctx {
  spaceId: string
  orgId: string
  token: string
  base: string
}

async function setup(page: Page, name: string): Promise<Ctx> {
  await createUserAndLogin(page)
  const spaceId = await createSpace(page, name, 'vector')
  const token = await getAuthToken(page)
  const { orgId } = await getCurrentUser(page)
  return { spaceId, orgId, token, base: `/api/v1/orgs/${orgId}/spaces/${spaceId}/projects` }
}

function headers(ctx: Ctx) {
  return { Authorization: `Bearer ${ctx.token}`, 'Content-Type': 'application/json' }
}

/** Creates an item and returns its id. */
async function apiItem(page: Page, ctx: Ctx, title: string, kind = 'task'): Promise<string> {
  const res = await page.request.post(`${ctx.base}/items`, {
    headers: headers(ctx),
    data: { title, kind, priority: 'medium' },
  })
  expect(res.status()).toBe(201)
  return (await res.json() as { id: string }).id
}

/** Creates a sprint, puts the given items on it, and starts it. */
async function apiActiveSprint(page: Page, ctx: Ctx, itemIds: string[]): Promise<string> {
  const res = await page.request.post(`${ctx.base}/sprints`, {
    headers: headers(ctx),
    data: { name: 'Board Sprint' },
  })
  expect(res.status()).toBe(201)
  const sprint = await res.json() as { id: string }

  for (const id of itemIds) {
    const assign = await page.request.post(`${ctx.base}/items/${id}/sprint`, {
      headers: headers(ctx),
      data: { sprint_id: sprint.id },
    })
    expect(assign.status()).toBe(200)
  }

  const start = await page.request.post(`${ctx.base}/sprints/${sprint.id}/start`, { headers: headers(ctx) })
  expect(start.status()).toBe(200)
  return sprint.id
}


/**
 * Opens the board-config editor and waits for its columns. The Card renders
 * while the config query is still loading, so a visible section is not yet a
 * usable editor — counting columns too early reads zero.
 */
async function openBoardConfig(page: Page) {
  const section = page.getByTestId('board-config-section')
  await expect(section).toBeVisible({ timeout: 10000 })
  await expect(section.getByTestId('board-config-column').first()).toBeVisible({ timeout: 10000 })
  return section
}

/**
 * Saves and waits for the write to actually land. Waiting on the button going
 * disabled is not enough: it disables the moment the mutation is pending, so
 * the assertion passes instantly and a following navigation aborts the PUT
 * mid-flight — which is exactly how this first went wrong.
 */
async function saveLayout(page: Page, section: ReturnType<Page['getByTestId']>) {
  const save = section.getByTestId('board-config-save')
  await expect(save).toBeEnabled()
  const done = page.waitForResponse(
    r => r.url().includes('/projects/board/config') && r.request().method() === 'PUT',
  )
  await save.click()
  const res = await done
  expect(res.status(), 'saving the board layout').toBe(200)
}

test.describe('Board customization', () => {
  test('default board is unchanged for a space that has never been customized', async ({ page }) => {
    // The regression protection every existing space depends on: with no saved
    // configuration the board renders the workflow states exactly as before.
    const ctx = await setup(page, 'Board Default')
    const a = await apiItem(page, ctx, 'Default Item')
    await apiActiveSprint(page, ctx, [a])

    await page.goto(`/vector/${ctx.spaceId}/board`)
    await expect(page.getByRole('heading', { level: 1, name: 'Board' })).toBeVisible({ timeout: 10000 })

    const columns = page.getByTestId('board-column')
    await expect(columns.first()).toBeVisible()
    const names = await columns.locator('h3').allTextContents()
    // Workflow-derived names, title-cased, one per status.
    expect(names.length).toBeGreaterThan(1)
    // Nothing carries a WIP limit by default.
    await expect(page.getByTestId('wip-overflow')).toHaveCount(0)
    await assertNoErrors(page)
  })

  test('customizing columns changes the board layout', async ({ page }) => {
    const ctx = await setup(page, 'Board Customize')
    const a = await apiItem(page, ctx, 'Layout Item')
    await apiActiveSprint(page, ctx, [a])

    await page.goto(`/vector/${ctx.spaceId}/settings`)
    const section = await openBoardConfig(page)

    // Rename the first column and save.
    await section.getByLabel('Column 1 name').fill('Fresh Start')
    await saveLayout(page, section)

    await page.goto(`/vector/${ctx.spaceId}/board`)
    await expect(page.getByRole('heading', { level: 1, name: 'Board' })).toBeVisible({ timeout: 10000 })
    await expect(page.getByTestId('board-column').locator('h3').first()).toHaveText('Fresh Start')
    await assertNoErrors(page)
  })

  test('a WIP limit renders as a soft warning and never blocks a drop', async ({ page }) => {
    const ctx = await setup(page, 'Board WIP')
    const ids = [
      await apiItem(page, ctx, 'WIP One'),
      await apiItem(page, ctx, 'WIP Two'),
      await apiItem(page, ctx, 'WIP Three'),
    ]
    await apiActiveSprint(page, ctx, ids)

    // Limit the first column to one, with three items already sitting in it.
    await page.goto(`/vector/${ctx.spaceId}/settings`)
    const section = await openBoardConfig(page)
    const firstName = await section.getByLabel('Column 1 name').inputValue()
    await section.getByLabel(`WIP limit for ${firstName}`).fill('1')
    await saveLayout(page, section)

    await page.goto(`/vector/${ctx.spaceId}/board`)
    const overflow = page.getByTestId('wip-overflow')
    await expect(overflow.first()).toBeVisible({ timeout: 10000 })

    // Soft, not hard: the column is flagged, the cards are all still there and
    // interactive. Nothing about the overflow disables the board.
    const overColumn = page.locator('[data-testid="board-column"][data-over-limit]').first()
    await expect(overColumn).toBeVisible()
    await expect(overColumn.getByTestId('column-count')).toContainText('/1')
    await expect(page.getByText('WIP One')).toBeVisible()

    // The card is still draggable — nothing is disabled or pointer-events:none.
    const card = page.getByText('WIP One')
    await expect(card).toBeEnabled()
    await assertNoErrors(page)
  })

  test('swimlanes group by assignee with an explicit catch-all lane', async ({ page }) => {
    const ctx = await setup(page, 'Board Lanes')
    const { userId, displayName } = await getCurrentUser(page)
    const assigned = await apiItem(page, ctx, 'Assigned Work')
    const loose = await apiItem(page, ctx, 'Unassigned Work')
    await apiActiveSprint(page, ctx, [assigned, loose])

    const patch = await page.request.patch(`${ctx.base}/items/${assigned}`, {
      headers: headers(ctx),
      data: { assignee_id: userId },
    })
    expect(patch.status()).toBe(200)

    await page.goto(`/vector/${ctx.spaceId}/board`)
    await expect(page.getByRole('heading', { level: 1, name: 'Board' })).toBeVisible({ timeout: 10000 })

    // No lanes to start with.
    await expect(page.getByTestId('board-lane')).toHaveCount(1)

    await page.getByRole('radio', { name: 'By assignee' }).click()

    const lanes = page.getByTestId('board-lane')
    await expect(lanes).toHaveCount(2)
    await expect(page.getByRole('heading', { level: 2, name: displayName })).toBeVisible()
    // Unassigned work gets its own visible lane — never silently hidden.
    await expect(page.getByRole('heading', { level: 2, name: 'Unassigned' })).toBeVisible()
    await expect(page.getByText('Unassigned Work')).toBeVisible()
    await assertNoErrors(page)
  })

  test('the type filter composes with swimlanes without double-filtering', async ({ page }) => {
    const ctx = await setup(page, 'Board Filter Lanes')
    const bug = await apiItem(page, ctx, 'A Bug Item', 'bug')
    const task = await apiItem(page, ctx, 'A Task Item', 'task')
    await apiActiveSprint(page, ctx, [bug, task])

    await page.goto(`/vector/${ctx.spaceId}/board`)
    await expect(page.getByRole('heading', { level: 1, name: 'Board' })).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('A Bug Item')).toBeVisible()
    await expect(page.getByText('A Task Item')).toBeVisible()

    // Lane by type first, then filter to bugs: the filter must narrow the
    // items, and the lanes must reflect the narrowed set — not keep an empty
    // Task lane around, and not drop the Bug lane.
    await page.getByRole('radio', { name: 'By type' }).click()
    await expect(page.getByTestId('board-lane')).toHaveCount(2)

    const typeFilter = page.getByTestId('type-filter')
    await typeFilter.getByRole('button', { name: 'Bug' }).click()

    await expect(page.getByText('A Bug Item')).toBeVisible()
    await expect(page.getByText('A Task Item')).not.toBeVisible()
    await expect(page.getByTestId('board-lane')).toHaveCount(1)

    // Clearing the filter restores both lanes — no sticky filter state.
    await typeFilter.getByRole('button', { name: 'Bug' }).click()
    await expect(page.getByTestId('board-lane')).toHaveCount(2)
    await expect(page.getByText('A Task Item')).toBeVisible()
    await assertNoErrors(page)
  })

  test('dragging a card into another type lane retypes the item on the server', async ({ page }) => {
    // T1. The by-type lane drop was a deliberate no-op while the item PATCH
    // carried no kind: the card snapped back rather than half-applying. With
    // the contract extended the drop has to reach the database.
    //
    // The assertion that counts is the API re-read at the end. The board keeps
    // an optimistic lane override for the duration of the request, so a
    // UI-only check passes on a change that never left the browser — which is
    // precisely the class of bug this journey exists to catch.
    const ctx = await setup(page, 'Board Type Drag')
    const mover = await apiItem(page, ctx, 'Retyped Item', 'task')
    const anchor = await apiItem(page, ctx, 'Anchor Bug', 'bug')
    await apiActiveSprint(page, ctx, [mover, anchor])

    /** Re-reads an item straight from the API — never through the board's cache. */
    const readItem = async (id: string) => {
      const res = await page.request.get(`${ctx.base}/items/${id}`, { headers: headers(ctx) })
      if (res.status() !== 200) return { kind: `HTTP ${res.status()}`, status: `HTTP ${res.status()}` }
      return await res.json() as { kind: string; status: string }
    }

    const before = await readItem(mover)
    expect(before.kind, 'seeded type before the drag').toBe('task')

    await page.goto(`/vector/${ctx.spaceId}/board`)
    await expect(page.getByRole('heading', { level: 1, name: 'Board' })).toBeVisible({ timeout: 10000 })
    await page.getByRole('radio', { name: 'By type' }).click()

    // Lane ids are the kind slugs, so the lanes are addressable without
    // depending on the org's display names for its types.
    const taskLane = page.locator('[data-testid="board-lane"][data-lane-id="task"]')
    const bugLane = page.locator('[data-testid="board-lane"][data-lane-id="bug"]')
    await expect(taskLane).toBeVisible()
    await expect(bugLane).toBeVisible()

    const card = taskLane.getByText('Retyped Item')
    await expect(card).toBeVisible()

    // Land in the *same* column of the other lane. A drop carries two axes;
    // holding the column fixed leaves the type as the only thing under test,
    // and lets the status assertion below mean something.
    const sourceColumn = taskLane.locator('[data-testid="board-column"]').filter({ hasText: 'Retyped Item' })
    const columnId = await sourceColumn.getAttribute('data-column-id')
    expect(columnId, 'the column the dragged card starts in').toBeTruthy()
    const targetColumn = bugLane.locator(`[data-column-id="${columnId}"]`)
    await expect(targetColumn).toBeVisible()

    // Same gesture the beacon board drag uses: dnd-kit's PointerSensor needs a
    // real press, a move past the 5px activation distance, then a glide.
    const cardBox = await card.boundingBox()
    const targetBox = await targetColumn.boundingBox()
    if (!cardBox || !targetBox) throw new Error('could not measure drag source/target')

    await page.mouse.move(cardBox.x + cardBox.width / 2, cardBox.y + cardBox.height / 2)
    await page.mouse.down()
    await page.mouse.move(cardBox.x + cardBox.width / 2 + 12, cardBox.y + cardBox.height / 2, { steps: 4 })
    await page.mouse.move(targetBox.x + targetBox.width / 2, targetBox.y + Math.min(120, targetBox.height / 2), { steps: 15 })
    await page.mouse.up()

    // Persisted, read back over a fresh request: the type actually changed.
    await expect
      .poll(async () => (await readItem(mover)).kind, {
        timeout: 10000,
        message: 'the dragged item\'s kind, re-read from the API after the drop',
      })
      .toBe('bug')

    // …and only the type. A lane drop into the same column must not also
    // rewrite the workflow status.
    expect((await readItem(mover)).status, 'status after a type-only drag').toBe(before.status)
    // The untouched card keeps its own type — the PATCH targeted one item.
    expect((await readItem(anchor)).kind, 'the item that was not dragged').toBe('bug')

    // The board agrees: both items are bugs now, so the Task lane is gone.
    await expect(bugLane.getByText('Retyped Item')).toBeVisible({ timeout: 10000 })
    await expect(page.getByTestId('board-lane')).toHaveCount(1)
    await assertNoErrors(page)
  })

  test('removing a column re-homes its statuses rather than orphaning them', async ({ page }) => {
    const ctx = await setup(page, 'Board Remove Column')
    const a = await apiItem(page, ctx, 'Rehomed Item')
    await apiActiveSprint(page, ctx, [a])

    await page.goto(`/vector/${ctx.spaceId}/settings`)
    const section = await openBoardConfig(page)

    const before = await section.getByTestId('board-config-column').count()
    const lastName = await section.getByLabel(`Column ${before} name`).inputValue()

    await section.getByLabel(`Remove ${lastName}`, { exact: true }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await page.getByTestId('board-config-confirm-remove').click()
    await expect(dialog).not.toBeVisible({ timeout: 10000 })

    await expect(section.getByTestId('board-config-column')).toHaveCount(before - 1)
    // Nothing is left unmapped: the removed column's statuses moved.
    await expect(section.getByTestId('board-config-unmapped')).not.toBeVisible()

    // And the board still renders every item.
    await page.goto(`/vector/${ctx.spaceId}/board`)
    await expect(page.getByText('Rehomed Item')).toBeVisible({ timeout: 10000 })
    await assertNoErrors(page)
  })

  test('the editor refuses to save a layout that would orphan a status', async ({ page }) => {
    const ctx = await setup(page, 'Board Orphan Guard')

    await page.goto(`/vector/${ctx.spaceId}/settings`)
    const section = await openBoardConfig(page)

    const firstName = await section.getByLabel('Column 1 name').inputValue()
    await section.getByLabel(`Remove ${firstName} from ${firstName}`).click()

    await expect(section.getByTestId('board-config-unmapped')).toBeVisible()
    await expect(section.getByTestId('board-config-save')).toBeDisabled()
  })

  test('resetting returns the space to the default layout', async ({ page }) => {
    const ctx = await setup(page, 'Board Reset')

    await page.goto(`/vector/${ctx.spaceId}/settings`)
    const section = await openBoardConfig(page)

    await section.getByLabel('Column 1 name').fill('Renamed')
    await saveLayout(page, section)
    await expect(section.getByLabel('Column 1 name')).toHaveValue('Renamed')

    await section.getByRole('button', { name: /Reset to default/i }).click()

    await expect(section.getByLabel('Column 1 name')).not.toHaveValue('Renamed', { timeout: 10000 })
    await expect(section.getByText(/uses the default layout/i)).toBeVisible()
    await assertNoErrors(page)
  })
})
