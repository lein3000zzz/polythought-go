import { expect, test, unique } from './fixtures'

const modelName = (prefix: string) =>
  unique(prefix)
    .toLowerCase()
    .replace(/[^a-z0-9_-]/g, '-')

// A model made on the Data page, a key for its worker, then the worker's loop over the API:
// it calls for a request, the model is asked, it answers, and the answer comes back.
test('a worker serves a model with its key, and is refused once the key is revoked', async ({
  page,
  playwright,
  baseURL,
}) => {
  const name = modelName('temps')

  await page.goto('/data/models')
  await page.getByRole('button', { name: 'New model' }).click()
  const dialog = page.getByRole('dialog', { name: 'New model' })
  await dialog.getByLabel('Name').fill(name)
  await dialog.getByRole('button', { name: 'Make it' }).click()

  const keys = page.getByRole('region', { name: `${name}'s worker keys` })
  await keys.getByLabel('New key').fill('e2e-worker')
  await keys.getByRole('button', { name: 'Make key' }).click()
  // The new key's own secret, not the page's Bearer pw_… hint.
  const secret = (await keys.getByRole('alert').locator('code').textContent())?.trim() ?? ''
  expect(secret).toMatch(/^pw_/)

  const worker = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${secret}` },
  })

  try {
    // Connected: its call waits for a request, which comes once the model is asked.
    const next = worker.post('/api/v1/worker/next', { data: { worker: 'e2e-1', version: '0.1.0' } })
    await expect
      .poll(async () => {
        const got = (await (await page.request.get(`/api/v1/models/${name}`)).json()) as {
          workers: unknown[]
        }
        return got.workers.length
      })
      .toBe(1)

    const asked = page.request.post(`/api/v1/models/${name}/ask`, {
      data: { items: [{ city: 'NYC' }], waitSeconds: 20 },
    })

    const took = await next
    expect(took.status(), await took.text()).toBe(200)
    const got = (await took.json()) as { model: string; request: { id: string; items: unknown[] } }
    expect(got.model).toBe(name)
    expect(got.request.items).toEqual([{ city: 'NYC' }])

    const answered = await worker.post(`/api/v1/worker/requests/${got.request.id}/answer`, {
      data: { worker: 'e2e-1', version: '0.1.0', answers: [{ high: 51.25 }] },
    })
    expect(answered.status(), await answered.text()).toBe(204)

    const result = await asked
    expect(result.status(), await result.text()).toBe(200)
    expect(await result.json()).toEqual({
      answers: [{ high: 51.25 }],
      version: '0.1.0',
      worker: 'e2e-1',
    })

    // The page shows the worker and the answer.
    await page.reload()
    const row = page
      .getByRole('table', { name: 'Models' })
      .getByRole('row', { name: new RegExp(name) })
    await expect(row).toContainText('1 connected: e2e-1 (0.1.0)')
    await expect(row).not.toContainText('Never')

    // A session or a token is no worker key.
    const asSession = await page.request.post('/api/v1/worker/next', { data: { worker: 'x' } })
    expect(asSession.status()).toBe(401)

    // Revoked, the key is refused from the next call.
    await row.getByRole('button', { name: 'Keys' }).click()
    await keys.getByRole('button', { name: 'Revoke e2e-worker' }).click()
    await expect(page.getByText('Revoked e2e-worker')).toBeVisible()

    const refused = await worker.post('/api/v1/worker/next', { data: { worker: 'e2e-1' } })
    expect(refused.status()).toBe(401)
  } finally {
    await worker.dispose()
    await page.request.delete(`/api/v1/models/${name}`)
  }
})
