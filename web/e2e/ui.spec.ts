import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

test('a dark theme is there before the app loads, with no white flash', async ({ page }) => {
  await page.goto('/workflows')
  await page.getByRole('button', { name: 'Theme' }).click()
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
