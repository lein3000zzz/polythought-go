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
        params: { credential: key, model: 'llama-e2e', prompt: 'Hello from {{ execution.id }}' },
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
