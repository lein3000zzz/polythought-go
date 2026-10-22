import type { Locator, Page } from '@playwright/test'

import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

test('a dark theme is there before the app loads, with no white flash', async ({ page }) => {
  await page.goto('/workflows')
  await page.getByRole('button', { name: 'Account' }).click()
  await page.getByRole('menuitemradio', { name: 'Dark' }).click()
  await expect(page.locator('html')).toHaveClass(/dark/)

  // Hold the app's code back: what shows is the page as first painted.
  await page.route('**/assets/*.js', (route) =>
    route.fulfill({ contentType: 'text/javascript', body: '' }),
  )
  await page.reload()

  await expect(page.locator('#root')).toBeEmpty() // the app did not run
  await expect(page.locator('html')).toHaveClass(/dark/)
  const background = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
  expect(background).not.toBe('rgb(255, 255, 255)')
})

test('the node list closes and stays closed', async ({ page }) => {
  const id = await createWorkflow(page, unique('roomy'), [{ id: 'manual', type: 'trigger.manual' }])

  await page.goto(`/workflows/${id}`)
  await expect(canvasNode(page, 'manual')).toBeVisible()
  const pane = page.locator('.react-flow__pane')
  const narrow = (await pane.boundingBox())!.width

  await page.getByRole('button', { name: 'Hide node list' }).click()
  await expect(page.getByRole('navigation', { name: 'Node types' })).toHaveCount(0)
  await expect.poll(async () => (await pane.boundingBox())!.width).toBeGreaterThan(narrow + 150)

  // Out of reach while closed: it cannot take focus, so Tab skips it.
  const focused = await page
    .locator('nav[aria-label="Node types"] button')
    .first()
    .evaluate((b: HTMLElement) => {
      b.focus()
      return document.activeElement === b
    })
  expect(focused).toBe(false)

  await page.reload()
  await expect(page.getByRole('button', { name: 'Show node list' })).toBeVisible()
  await page.getByRole('button', { name: 'Show node list' }).click()
  await expect(page.getByRole('navigation', { name: 'Node types' })).toBeVisible()
})

// Drags a panel's edge by dx, as a person would, from the middle of its strip.
async function dragEdge(page: Page, name: string, dx: number) {
  const box = (await page.getByRole('separator', { name }).boundingBox())!
  const [x, y] = [box.x + box.width / 2, box.y + box.height / 2]

  await page.mouse.move(x, y)
  await page.mouse.down()
  await page.mouse.move(x + dx, y, { steps: 5 })
  await page.mouse.up()
}

test('the side panels drag wider, stay so, and go back on a double-click (D369)', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('wide'), [{ id: 'manual', type: 'trigger.manual' }])

  await page.goto(`/workflows/${id}`)
  await expect(canvasNode(page, 'manual')).toBeVisible()
  const list = page.getByRole('complementary', { name: 'Node list' })
  const settings = page.getByRole('complementary', { name: 'Settings' })
  const width = (panel: Locator) => async () => (await panel.boundingBox())!.width

  await dragEdge(page, 'Resize the node list', 100)
  await expect.poll(width(list)).toBe(340)
  await dragEdge(page, 'Resize the settings', -100) // it grows leftwards
  await expect.poll(width(settings)).toBe(420)

  await page.reload()
  await expect(canvasNode(page, 'manual')).toBeVisible()
  await expect.poll(width(list)).toBe(340)
  await expect.poll(width(settings)).toBe(420)

  await page.getByRole('separator', { name: 'Resize the node list' }).dblclick()
  await expect.poll(width(list)).toBe(240)
  await page.getByRole('separator', { name: 'Resize the settings' }).dblclick()
  await expect.poll(width(settings)).toBe(320)
})

test('a double-clicked node opens the folded settings on it, and the canvas does not zoom (D390)', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('folded'), [
    { id: 'manual', type: 'trigger.manual' },
  ])

  await page.goto(`/workflows/${id}`)
  await expect(canvasNode(page, 'manual')).toBeVisible()
  await page.getByRole('button', { name: 'Hide settings' }).click()
  await expect(page.getByRole('button', { name: 'Show settings' })).toBeVisible()

  const viewport = page.locator('.react-flow__viewport')
  const before = await viewport.getAttribute('style')

  await canvasNode(page, 'manual').dblclick()
  await expect(page.getByRole('button', { name: 'Hide settings' })).toBeVisible()
  await expect(
    page.getByRole('complementary', { name: 'Settings' }).getByRole('heading', { name: 'Manual' }),
  ).toBeVisible()
  expect(await viewport.getAttribute('style')).toBe(before)

  // The empty canvas still zooms on a double-click: the node's was kept from it.
  const pane = (await page.locator('.react-flow__pane').boundingBox())!
  await page.mouse.dblclick(pane.x + pane.width - 40, pane.y + 40)
  await expect.poll(() => viewport.getAttribute('style')).not.toBe(before)
})

test('the node list searches, and / gets there from anywhere', async ({ page }) => {
  await page.goto('/workflows/new')
  await expect(canvasNode(page, 'manual')).toBeVisible()

  await page.locator('.react-flow__pane').click()
  await page.keyboard.press('/')
  const search = page.getByRole('searchbox', { name: 'Search nodes' })
  await expect(search).toBeFocused()

  await search.fill('discord slack')
  const list = page.getByRole('navigation', { name: 'Node types' })
  await expect(list.getByRole('button')).toHaveCount(1)
  await expect(list.getByText('Webhook', { exact: true })).toBeVisible()

  await search.press('Enter')
  await expect(canvasNode(page, 'webhook')).toBeVisible()
  await expect(search).toHaveValue('')
})
