import type { Locator, Page } from '@playwright/test'

import { canvasNode, expect, test } from './fixtures'

// Scrollbars that take room, as on Windows, Linux, or macOS set to show them always:
// headless Chromium hides them otherwise.
test.use({ launchOptions: { ignoreDefaultArgs: ['--hide-scrollbars'] } })

const box = async (page: Page, path: string, what: (page: Page) => Locator) => {
  await page.goto(path)
  await page.waitForLoadState('networkidle')
  const b = (await what(page).boundingBox())!

  return `${Math.round(b.x)} ${Math.round(b.width)}`
}

test('Settings and the admin pages keep their place, short or tall', async ({ page }) => {
  const settings = (p: Page) => p.getByRole('navigation', { name: 'Settings pages' })
  const places = new Set<string>()
  // Notifications is tall enough to scroll; the others are not.
  for (const slug of ['', 'account', 'telegram', 'notifications', 'tokens', 'credentials']) {
    places.add(await box(page, `/settings/${slug}`, settings))
  }
  expect([...places]).toHaveLength(1)

  // One width for every tab, Config's tall pages among them.
  const tabs = (p: Page) => p.getByRole('navigation', { name: 'Admin pages' })
  places.clear()
  for (const path of [
    '/admin',
    '/admin/invites',
    '/admin/config',
    '/admin/config/accounts',
    '/admin/plugins',
  ]) {
    places.add(await box(page, path, tabs))
  }
  expect([...places]).toHaveLength(1)
})

test("the editor's canvas keeps no room for a scrollbar it never has", async ({ page }) => {
  await page.goto('/workflows/new')
  await expect(canvasNode(page, 'manual')).toBeVisible()

  const gutter = await page
    .locator('main')
    .evaluate((m: HTMLElement) => m.offsetWidth - m.clientWidth)
  expect(gutter).toBe(0)
})
