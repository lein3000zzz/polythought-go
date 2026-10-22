import { canvasNode, expect, test, unique } from './fixtures'

test('the bot follows its settings, and Settings says what it is missing', async ({ page }) => {
  await page.goto('/settings')
  await expect(page.getByText('Telegram is not set up on this instance.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Link Telegram' })).toHaveCount(0)

  // Switched on without a token, the bot cannot start, and says why. No restart needed.
  await page.goto('/admin/config/telegram')
  await page.getByLabel('Telegram bot').click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()

  await page.goto('/settings')
  await expect(page.getByText('The bot reported a problem')).toBeVisible()
  await expect(page.getByText(/there is no bot token; add one on the Config page/)).toBeVisible()

  // Off again, for the other tests.
  await page.goto('/admin/config/telegram')
  await page.getByLabel('Telegram bot').click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()

  await page.goto('/settings')
  await expect(page.getByText('Telegram is not set up on this instance.')).toBeVisible()
  await expect(page.getByText('The bot reported a problem')).toHaveCount(0)
})

test('notification choices are kept, and the editor offers the Telegram nodes', async ({
  page,
}) => {
  await page.goto('/settings')
  const failures = page.getByRole('switch', { name: 'A workflow fails, on Telegram' })
  await expect(failures).toBeChecked()
  await expect(page.getByText(/link Telegram above to get them/)).toBeVisible()

  await failures.click()
  await expect(failures).not.toBeChecked()
  await page.reload()
  await expect(
    page.getByRole('switch', { name: 'A workflow fails, on Telegram' }),
  ).not.toBeChecked()

  await page.getByRole('switch', { name: 'A workflow fails, on Telegram' }).click()
  await expect(page.getByRole('switch', { name: 'A workflow fails, on Telegram' })).toBeChecked()

  await page.goto('/workflows/new')
  const list = page.getByRole('navigation', { name: 'Node types' })
  await expect(list.getByText('Telegram message', { exact: true })).toHaveCount(2) // trigger and node
  await expect(list.getByRole('heading', { name: 'Telegram' })).toBeVisible()
})

test('a question has a port per button, which keeps its line when renamed', async ({ page }) => {
  const created = await page.request.post('/api/v1/workflows', {
    data: {
      name: unique('question'),
      nodes: [
        { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
        {
          id: 'ask',
          type: 'telegram.ask',
          params: { buttons: ['Buy', 'Skip'] },
          position: { x: 300, y: 0 },
        },
        { id: 'log', type: 'output.log', position: { x: 600, y: 0 } },
      ],
      connections: [
        { from: 'manual', fromPort: 'main', to: 'ask', toPort: 'main' },
        { from: 'ask', fromPort: 'Buy', to: 'log', toPort: 'main' },
      ],
    },
  })
  expect(created.status(), await created.text()).toBe(201)
  const { id } = (await created.json()) as { id: string }

  await page.goto(`/workflows/${id}`)
  const ask = canvasNode(page, 'ask')
  for (const port of ['timeout', 'Buy', 'Skip']) {
    await expect(ask.locator(`.react-flow__handle.source[data-handleid="${port}"]`)).toBeVisible()
  }

  // Buy is typed over; its line follows, and the server takes the new port.
  await ask.click()
  const buttons = page.getByRole('complementary', { name: 'Settings' }).getByLabel(/Buttons/)
  await expect(buttons).toHaveValue('Buy\nSkip')
  await buttons.focus()
  await buttons.evaluate((el: HTMLTextAreaElement) => el.setSelectionRange(0, 3))
  await page.keyboard.type('Buy now')
  await expect(ask.locator('.react-flow__handle.source[data-handleid="Buy now"]')).toBeVisible()

  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()

  const saved = (await (await page.request.get(`/api/v1/workflows/${id}`)).json()) as {
    connections: { from: string; fromPort: string; to: string }[]
  }
  expect(saved.connections).toContainEqual(
    expect.objectContaining({ from: 'ask', fromPort: 'Buy now', to: 'log' }),
  )

  // Without a linked chat there is nobody to ask.
  await page.getByRole('button', { name: 'Run', exact: true }).click()
  await expect(canvasNode(page, 'ask').getByLabel('Failed')).toBeVisible()
  await expect(page.getByText(/telegram is not linked/).first()).toBeVisible()

  await page.goto('/settings')
  await expect(
    page.getByRole('switch', { name: 'A question waits for your answer, on Telegram' }),
  ).toBeChecked()
})
