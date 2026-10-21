import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

test('a credential is made once, picked in a webhook node, and kept private', async ({ page }) => {
  const name = unique('relay')

  await page.goto('/credentials')
  await page.getByRole('button', { name: 'New credential' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Kind').selectOption({ label: 'HTTP header' })
  await dialog.getByLabel('Name').fill(name)
  await dialog.getByLabel('Header').fill('Authorization')
  await dialog.getByLabel('Value').fill('Bearer e2e-secret')
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(page.getByText(`Created ${name}`)).toBeVisible()

  const row = page.getByRole('row', { name: new RegExp(name) })
  await expect(row.getByText('Header: Authorization')).toBeVisible()
  await expect(row.getByText('Value saved')).toBeVisible()

  // The secret never comes back.
  const listed = await page.request.get('/api/v1/credentials')
  expect(await listed.text()).not.toContain('e2e-secret')

  // A webhook node picks it.
  const id = await createWorkflow(page, unique('authed'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'hook', type: 'output.webhook', params: { url: 'https://example.com/hook' } },
  ])

  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'hook').click()
  const settings = page.getByRole('complementary', { name: 'Settings' })
  await settings.getByLabel('Credential').selectOption({ label: name })
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()

  // Sharing leaves it out, and whoever uses the copy may pick their own.
  const shared = await page.request.put(`/api/v1/workflows/${id}/share`, {
    data: { summary: 'Posts with a header' },
  })
  expect(shared.status()).toBe(200)
  const template = (await shared.json()) as {
    id: string
    inputs: { nodeId: string; param: { name: string } }[]
  }
  expect(template.inputs).toContainEqual(
    expect.objectContaining({
      nodeId: 'hook',
      param: expect.objectContaining({ name: 'credential' }),
    }),
  )

  await page.goto(`/library/${template.id}`)
  await expect(page.getByText('Credential (optional)')).toBeVisible()
  await page.getByRole('button', { name: 'Use this' }).click()
  await expect(page.getByRole('dialog').getByLabel('Credential')).toHaveValue('')

  // Deleted, the node shows it is gone.
  await page.goto('/credentials')
  await row.getByRole('button', { name: `Delete ${name}` }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText(`Deleted ${name}`)).toBeVisible()

  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'hook').click()
  await expect(settings.getByLabel('Credential')).toContainText('Missing credential')
})

test('a language model key is checked as saved, and its test sends nothing in clear', async ({
  page,
}) => {
  const name = unique('router')

  await page.goto('/credentials')
  await page.getByRole('button', { name: 'New credential' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Kind').selectOption({ label: 'OpenAI-compatible API key' })
  await dialog.getByLabel('Name').fill(name)
  await dialog.getByLabel('API key').fill('sk-e2e-secret')
  await dialog.getByLabel('Base URL').fill('openrouter.ai')
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(dialog.getByText(/the base URL must be an http or https address/)).toBeVisible()

  // A server on the network, in plain http: this instance keeps to public addresses, so
  // the test refuses before anything is sent.
  await dialog.getByLabel('Base URL').fill('http://localhost:11434/v1')
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(page.getByText(`Created ${name}`)).toBeVisible()

  const row = page.getByRole('row', { name: new RegExp(name) })
  await expect(row.getByText('Base URL: http://localhost:11434/v1')).toBeVisible()
  await expect(row.getByText('API key saved')).toBeVisible()
  await row.getByRole('button', { name: 'Test' }).click()
  await expect(row.getByText(/Failed: the base URL must be https/)).toBeVisible()

  await row.getByRole('button', { name: `Delete ${name}` }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText(`Deleted ${name}`)).toBeVisible()
})
