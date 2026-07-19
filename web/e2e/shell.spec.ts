import { test, expect, type Page } from '@playwright/test'
import { createUserAndLogin, createSpace } from './helpers/setup'

// P1 DoD (spec §9, ADR-0005): the sidebar is present and correct on EVERY
// module sub-route — Sprints and Roadmap included — and no route in the
// application renders an empty document body. The module sidebar derives
// from the :module URL segment, never from the sub-route.

const SUB_ROUTES = ['board', 'backlog', 'sprints', 'roadmap', 'labels', 'settings'] as const

const MODULES = [
  { key: 'beacon', type: 'beacon', distinctive: 'Tickets' },
  { key: 'codex', type: 'codex', distinctive: 'Pages' },
  { key: 'vector', type: 'vector', distinctive: 'Backlog' },
] as const

async function assertSidebarAndBody(page: Page, moduleKey: string, distinctive: string) {
  const sidebar = page.getByTestId('space-sidebar')
  await expect(sidebar).toBeVisible({ timeout: 10000 })
  await expect(sidebar).toHaveAttribute('data-module', moduleKey)
  await expect(sidebar.getByText(distinctive, { exact: true })).toBeVisible()
  await expect(sidebar.getByText('Settings', { exact: true })).toBeVisible()
  expect((await page.locator('body').innerText()).trim()).not.toBe('')
}

test.describe('Navigation shell', () => {
  for (const mod of MODULES) {
    test(`${mod.key}: sidebar present and correct on every sub-route`, async ({ page }) => {
      await createUserAndLogin(page)
      const spaceId = await createSpace(page, `Shell ${mod.key}`, mod.type)

      for (const sub of SUB_ROUTES) {
        await page.goto(`/${mod.key}/${spaceId}/${sub}`)
        await assertSidebarAndBody(page, mod.key, mod.distinctive)
      }
    })
  }

  test('top bar shows product tabs and the space picker opens with search', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Shell Picker', 'vector')

    for (const tab of ['home', 'beacon', 'codex', 'vector']) {
      await expect(page.getByTestId(`product-tab-${tab}`)).toBeVisible()
    }

    await page.goto(`/vector/${spaceId}/backlog`)
    await page.getByTestId('space-picker-button').click()
    const picker = page.getByTestId('space-picker')
    await expect(picker).toBeVisible()
    const search = page.getByTestId('space-picker-search')
    await expect(search).toBeFocused()

    // Searching for garbage shows the branded empty message, not a blank panel.
    await search.fill('zzz-no-such-space')
    await expect(picker.getByText('No spaces match that.')).toBeVisible()
  })

  test('sidebar collapses to an icon rail and the state persists across reload', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Shell Collapse', 'vector')
    await page.goto(`/vector/${spaceId}/backlog`)

    const sidebar = page.getByTestId('space-sidebar')
    await expect(sidebar).toBeVisible()
    await expect(sidebar).not.toHaveAttribute('data-collapsed', 'true')

    await page.getByTestId('sidebar-collapse').click()
    await expect(sidebar).toHaveAttribute('data-collapsed', 'true')

    await page.reload()
    await expect(page.getByTestId('space-sidebar')).toHaveAttribute('data-collapsed', 'true', {
      timeout: 10000,
    })

    // Restore for other tests sharing this storage state.
    await page.getByTestId('sidebar-collapse').click()
    await expect(page.getByTestId('space-sidebar')).not.toHaveAttribute('data-collapsed', 'true')
  })

  test('module chip renders neutral foreground — never the module hue', async ({ page }) => {
    // Spec §8: hue with neutral text means provenance. --module-chip-fg is
    // #A6AEBC → rgb(166, 174, 188). The chip on the Home space card must
    // compute exactly that colour, not the module hue.
    await createUserAndLogin(page)
    await createSpace(page, 'Chip Colour', 'beacon')
    await page.goto('/')

    const chip = page.getByTestId('module-chip').first()
    await expect(chip).toBeVisible({ timeout: 10000 })
    const color = await chip.evaluate((el) => getComputedStyle(el).color)
    expect(color).toBe('rgb(166, 174, 188)')

    const bg = await chip.evaluate((el) => getComputedStyle(el).backgroundColor)
    expect(bg).not.toBe('rgba(0, 0, 0, 0)')
  })

  test('unknown sub-route inside a space keeps the sidebar and renders a branded empty state', async ({ page }) => {
    await createUserAndLogin(page)
    const spaceId = await createSpace(page, 'Shell Unknown Route', 'vector')

    await page.goto(`/vector/${spaceId}/definitely-not-a-route`)
    await expect(page.getByTestId('space-sidebar')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('Nothing here')).toBeVisible()
    expect((await page.locator('body').innerText()).trim()).not.toBe('')
  })

  test('post-login landing is the Home overview', async ({ page }) => {
    await createUserAndLogin(page)
    await expect(page).toHaveURL('/')
    await expect(page.getByTestId('home-sidebar')).toBeVisible()
    await expect(page.getByTestId('home-sidebar').getByText('Your work')).toBeVisible()
  })
})
