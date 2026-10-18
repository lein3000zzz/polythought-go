import { canvasNode, connect, createWorkflow, expect, fillSetting, test, unique } from './fixtures'

test('building, saving and running a workflow', async ({ page }) => {
  const name = unique('built')

  await page.goto('/workflows')
  await page.getByRole('link', { name: 'New workflow' }).click()
  await expect(canvasNode(page, 'manual')).toBeVisible()

  // Zoom out for room, then drop a Log node to the right of the trigger.
  await page.getByRole('button', { name: 'Zoom Out' }).click()
  const pane = page.locator('.react-flow__pane')
  const box = (await pane.boundingBox())!
  await page
    .getByRole('navigation', { name: 'Node types' })
    .getByText('Log', { exact: true })
    .dragTo(pane, { targetPosition: { x: box.width * 0.8, y: box.height * 0.5 } })

  await connect(page, 'manual', 'main', 'log')
  await expect(page.locator('.react-flow__edge')).toHaveCount(1)

  const settings = page.getByRole('complementary', { name: 'Settings' })
  await canvasNode(page, 'log').click()
  await fillSetting(settings, 'message', 'hello from e2e')
  await page.getByLabel('Workflow name').fill(name)
  await page.keyboard.press('ControlOrMeta+s')

  await expect(page).toHaveURL(/\/workflows\/[0-9a-f-]{36}$/)
  await expect(page.getByText('Version 1')).toBeVisible()

  await page.getByRole('button', { name: 'Run', exact: true }).click()
  await expect(page.getByText(/Execution at/)).toBeVisible()
  await expect(canvasNode(page, 'log').getByLabel('Succeeded')).toBeVisible()

  await canvasNode(page, 'log').click()
  await expect(settings.getByRole('tab', { name: 'Output' })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(settings.getByText('main · 1 item')).toBeVisible()

  // The list shows it after a reload.
  await page.reload()
  await expect(page.getByLabel('Workflow name')).toHaveValue(name)
})

test('the server points at what is wrong', async ({ page }) => {
  const id = await createWorkflow(page, unique('problems'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'markets', type: 'polymarket.markets' },
    { id: 'books', type: 'polymarket.orderbook' },
    { id: 'tight', type: 'logic.if', params: { field: 'spread', value: '0.02' } },
  ])

  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'markets').click()

  const settings = page.getByRole('complementary', { name: 'Settings' })
  await fillSetting(settings, 'limit', 'lots')
  await expect(page.getByText('Enter a whole number')).toBeVisible()

  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/Not saved: the workflow has problems/)).toBeVisible()
  await expect(canvasNode(page, 'markets').getByText('1')).toBeVisible()

  await fillSetting(settings, 'limit', '5')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()
})

test('a filter takes more tests, and Match says how they count', async ({ page }) => {
  const id = await createWorkflow(page, unique('tests'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'markets', type: 'polymarket.markets' },
    { id: 'books', type: 'polymarket.orderbook' },
    {
      id: 'pick',
      type: 'logic.filter',
      params: { field: 'spread', operator: 'lt', value: '0.02' },
    },
  ])

  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'pick').click()

  const settings = page.getByRole('complementary', { name: 'Settings' })
  await settings.getByRole('button', { name: 'Edit more tests' }).click()
  const dialog = page.getByRole('dialog', { name: 'More tests' })
  await dialog.getByRole('button', { name: 'Add a row' }).click()
  await dialog.getByRole('textbox', { name: 'Field, row 1' }).fill('question')
  await dialog.getByRole('combobox', { name: 'Test, row 1' }).selectOption('contains')
  await dialog.getByRole('textbox', { name: 'Value, row 1' }).fill('bitcoin')
  await dialog.getByRole('button', { name: 'Done' }).click()
  await settings.getByLabel('Match', { exact: true }).selectOption('any')

  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()

  const saved = (await (await page.request.get(`/api/v1/workflows/${id}`)).json()) as {
    nodes: { id: string; params?: Record<string, unknown> }[]
  }
  expect(saved.nodes.find((n) => n.id === 'pick')?.params).toEqual({
    field: 'spread',
    operator: 'lt',
    value: '0.02',
    tests: [{ field: 'question', operator: 'contains', value: 'bitcoin' }],
    match: 'any',
  })
})

test('a save based on an old version is refused', async ({ page, context }) => {
  const id = await createWorkflow(page, unique('conflict'), [
    { id: 'manual', type: 'trigger.manual' },
  ])

  await page.goto(`/workflows/${id}`)
  await expect(page.getByText('Version 1')).toBeVisible()

  const other = await context.newPage()
  await other.goto(`/workflows/${id}`)
  await other.getByLabel('Workflow name').fill('saved in another tab')
  await other.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(other.getByText('Version 2')).toBeVisible()
  await other.close()

  await page.getByLabel('Workflow name').fill('saved here')
  await page.getByRole('button', { name: 'Save', exact: true }).click()

  const dialog = page.getByRole('alertdialog')
  await expect(dialog.getByText('This workflow changed elsewhere')).toBeVisible()
  await dialog.getByRole('button', { name: 'Reload' }).click()
  await expect(page.getByLabel('Workflow name')).toHaveValue('saved in another tab')
})

test('field settings offer the fields there, and saving checks them', async ({ page }) => {
  const nodes = (field: string) => [
    { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
    { id: 'markets', type: 'polymarket.markets', position: { x: 300, y: 0 } },
    { id: 'deep', type: 'transform.top', params: { field }, position: { x: 600, y: 0 } },
  ]
  const connections = [
    { from: 'manual', fromPort: 'main', to: 'markets', toPort: 'main' },
    { from: 'markets', fromPort: 'main', to: 'deep', toPort: 'main' },
  ]

  // The API checks too: a field the items lack is refused, with the one meant.
  const refused = await page.request.post('/api/v1/workflows', {
    data: { name: unique('fields'), nodes: nodes('liquidty'), connections },
  })
  expect(refused.status()).toBe(400)
  expect(await refused.text()).toContain('no field \\"liquidty\\"; \\"liquidity\\"?')

  const created = await page.request.post('/api/v1/workflows', {
    data: { name: unique('fields'), nodes: nodes('liquidity'), connections },
  })
  expect(created.status(), await created.text()).toBe(201)
  const { id } = (await created.json()) as { id: string }

  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'deep').click()
  const field = page.getByRole('complementary', { name: 'Settings' }).getByLabel(/^Sort by/)

  // Top sorts by a number: only the markets' numbers are offered.
  const offered = await page
    .locator(`#${await field.getAttribute('list')} option`)
    .evaluateAll((options) => options.map((o) => o.getAttribute('value')))
  expect(offered).toContain('volume24h')
  expect(offered).toContain('outcomes.0.price')
  expect(offered).not.toContain('question')

  await field.fill('liquidty')
  await expect(
    page.getByText('The items it receives have no field "liquidty"; "liquidity"?'),
  ).toBeVisible()

  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/Not saved: the workflow has problems/)).toBeVisible()

  await field.fill('volume24h')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()
})
