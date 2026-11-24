import { createWorkflow, expect, test, unique } from './fixtures'

const modelName = (prefix: string) =>
  unique(prefix)
    .toLowerCase()
    .replace(/[^a-z0-9_-]/g, '-')

// A workflow asks a model its worker serves, and the run waits for the answer; then the
// model deleted switches the workflow off, and it stays off.
test('a workflow asks a model, its worker answers, until the model is deleted', async ({
  page,
  playwright,
  baseURL,
}) => {
  const name = modelName('asked')
  const made = await page.request.post('/api/v1/models', { data: { name, kind: 'worker' } })
  expect(made.status(), await made.text()).toBe(201)

  const key = await page.request.post(`/api/v1/models/${name}/keys`, { data: { name: 'e2e' } })
  expect(key.status(), await key.text()).toBe(201)
  const secret = ((await key.json()) as { secret: string }).secret

  const workflow = unique('ask')
  const id = await createWorkflow(page, workflow, [
    { id: 'every', type: 'trigger.schedule', params: { interval: '1h', runOnStart: false } },
    { id: 'ask', type: 'data.ask', params: { model: name, wait: '20s' } },
  ])

  const worker = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${secret}` },
  })

  try {
    const next = worker.post('/api/v1/worker/next', { data: { worker: 'e2e-1', version: '0.2.0' } })
    await expect
      .poll(async () => {
        const got = (await (await page.request.get(`/api/v1/models/${name}`)).json()) as {
          workers: unknown[]
        }
        return got.workers.length
      })
      .toBe(1)

    // Run by hand, the run waits for the worker's answer.
    const run = page.request.post(`/api/v1/workflows/${id}/run`)

    const took = await next
    expect(took.status(), await took.text()).toBe(200)
    const got = (await took.json()) as { request: { id: string; items: unknown[] } }
    expect(got.request.items).toHaveLength(1)

    const answered = await worker.post(`/api/v1/worker/requests/${got.request.id}/answer`, {
      data: { worker: 'e2e-1', version: '0.2.0', answers: [{ high: 51.25 }] },
    })
    expect(answered.status(), await answered.text()).toBe(204)

    const ran = await run
    expect(ran.status(), await ran.text()).toBe(200)
    const result = (await ran.json()) as {
      status: string
      runs: { nodeId: string; output?: Record<string, number> }[]
    }
    expect(result.status).toBe('succeeded')
    expect(result.runs.find((r) => r.nodeId === 'ask')?.output).toEqual({ main: 1 })
  } finally {
    await worker.dispose()
  }

  expect((await page.request.post(`/api/v1/workflows/${id}/activate`)).status()).toBe(200)

  // Deleting the model names the workflow, and switches it off.
  await page.goto('/data/models')
  const row = page
    .getByRole('table', { name: 'Models' })
    .getByRole('row', { name: new RegExp(name) })
  await expect(row).not.toContainText('Never')
  await row.getByRole('button', { name: `Delete ${name}` }).click()
  const confirm = page.getByRole('alertdialog')
  await expect(confirm).toContainText(`“${workflow}” (on) asks it: those on are switched off.`)
  await confirm.getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText(`Deleted ${name}`)).toBeVisible()

  await expect
    .poll(async () => {
      const after = (await (await page.request.get(`/api/v1/workflows/${id}`)).json()) as {
        active: boolean
      }
      return after.active
    })
    .toBe(false)

  const refused = await page.request.post(`/api/v1/workflows/${id}/activate`)
  expect(refused.status()).toBe(400)
  expect(await refused.text()).toContain(`names the model \\"${name}\\", which you do not have`)
})
