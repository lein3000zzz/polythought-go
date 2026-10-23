import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

// Nothing reaches a provider here (D252): the key's base URL is plain http, which this
// instance, keeping to public addresses, refuses before it connects. Listing its models
// fails with a 502, the provider's failure, which the model field shows.
test.use({ expectedConsoleError: /status of 502 \(Bad Gateway\)/ })

test('an AI agent asks with your own key, and says why it could not', async ({ page }) => {
  const created = await page.request.post('/api/v1/credentials', {
    data: {
      type: 'openai.apiKey',
      name: unique('local model'),
      values: { apiKey: 'sk-e2e-secret', baseUrl: 'http://localhost:11434/v1' },
    },
  })
  expect(created.status(), await created.text()).toBe(201)
  const key = ((await created.json()) as { id: string }).id

  try {
    const id = await createWorkflow(page, unique('agent'), [
      { id: 'manual', type: 'trigger.manual' },
      {
        id: 'ask',
        type: 'ai.agent',
        params: {
          credential: key,
          model: 'llama-e2e',
          prompt: 'Hello from {{ execution.id }}',
          tools: ['searchMarkets', 'orderBook'],
        },
      },
    ])

    const ran = await page.request.post(`/api/v1/workflows/${id}/run`)
    expect(ran.status()).toBe(200)
    const run = (await ran.json()) as { status: string; error?: string }
    expect(run.status).toBe('failed')
    expect(run.error).toContain('the base URL must be https')
    expect(run.error).not.toContain('sk-e2e-secret')

    // In the editor, the key is picked, and its models could not be listed, for the same
    // reason; any model's name can still be typed.
    await page.goto(`/workflows/${id}`)
    await canvasNode(page, 'ask').click()
    const settings = page.getByRole('complementary', { name: 'Settings' })
    await expect(settings.getByLabel('Key')).toHaveValue(key)
    await expect(settings.getByLabel('Model')).toHaveValue('llama-e2e')
    await expect(
      settings.getByText(/Its models could not be listed: .*the base URL must be https/),
    ).toBeVisible()
    await expect(settings.getByRole('textbox', { name: /^Prompt/ })).toHaveValue(
      'Hello from {{ execution.id }}',
    )

    // The tools it may use, picked from the five, read-only.
    await expect(settings.getByRole('checkbox', { name: 'searchMarkets' })).toBeChecked()
    await expect(settings.getByRole('checkbox', { name: 'orderBook' })).toBeChecked()
    await expect(settings.getByRole('checkbox', { name: 'positions' })).not.toBeChecked()
    await expect(settings.getByLabel('Tool calls')).toBeVisible()

    // The library's scenario asks for a key and a model of your own.
    await page.goto('/library')
    await page.getByRole('link', { name: /Markets a model would watch/ }).click()
    await expect(page.getByText(/billed to that key's account/)).toBeVisible()
    await page.getByRole('button', { name: 'Use this' }).click()
    const dialog = page.getByRole('dialog')
    await dialog.getByLabel('Key').selectOption(key)
    await expect(dialog.getByLabel('Model')).toBeVisible()
    await expect(dialog.getByText(/Its models could not be listed/)).toBeVisible()
  } finally {
    await page.request.delete(`/api/v1/credentials/${key}`)
  }
})

test("agents' spend this month shows on Settings, against caps an admin sets (D271, D276, D277)", async ({
  page,
}) => {
  const me = (await (await page.request.get('/api/v1/me')).json()) as { id: string; email: string }

  try {
    // Nothing spent, as no provider is ever called here, against the instance's caps.
    await page.goto('/settings/agents')
    const spend = page.getByLabel('Agent spend', { exact: true })
    await expect(spend).toContainText('$0.00 of $20.00')
    await expect(spend).toContainText('0 of 20,000,000')

    // Caps of their own, on the Users page, empty for the instance's.
    await page.goto('/admin')
    const row = page.getByRole('row', { name: new RegExp(me.email) })
    await row.getByRole('button', { name: `Agent caps of ${me.email}` }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText(/This month: \$0\.00/)).toBeVisible()
    await expect(dialog.getByLabel('Max $ a month')).toHaveAttribute(
      'placeholder',
      'Default: $20.00',
    )
    await dialog.getByLabel('Max $ a month').fill('5.50')
    await dialog.getByLabel('Max output tokens a month').fill('1000')
    await dialog.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText(`Agent caps of ${me.email} saved`)).toBeVisible()
    await expect(row.getByRole('button', { name: `Agent caps of ${me.email}` })).toHaveText(
      '$5.50 a month, 1,000 out',
    )

    await page.goto('/settings/agents')
    await expect(spend).toContainText('$0.00 of $5.50')
    await expect(spend).toContainText('Output tokens0 of 1,000')

    // Whether the kill switch stops agents too is their own choice, kept.
    const choice = page.getByRole('switch', { name: 'My kill switch stops my agents too' })
    await expect(choice).not.toBeChecked()
    await choice.click()
    await expect(choice).toBeChecked()
    await page.reload()
    await expect(choice).toBeChecked()
  } finally {
    await page.request.put('/api/v1/me/agents', { data: { killStopsAgents: false } })
    await page.request.patch(`/api/v1/admin/users/${me.id}`, { data: { agentCaps: {} } })
  }
})
