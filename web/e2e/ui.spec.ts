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

  await page.reload()
  await expect(page.getByRole('button', { name: 'Show node list' })).toBeVisible()
  await page.getByRole('button', { name: 'Show node list' }).click()
  await expect(page.getByRole('navigation', { name: 'Node types' })).toBeVisible()
})
