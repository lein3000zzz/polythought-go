import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

const datasetName = (prefix: string) =>
  unique(prefix)
    .toLowerCase()
    .replace(/[^a-z0-9_-]/g, '-')

// A workflow records into a dataset and reads it back; its dataset picked in the editor, or
// made there; then the dataset deleted switches it off, and it stays off.
test('a workflow records into a dataset and reads it, until the dataset is deleted', async ({
  page,
}) => {
  const name = datasetName('seen')
  const made = await page.request.post('/api/v1/datasets', { data: { name, keepDays: 90 } })
  expect(made.status(), await made.text()).toBe(201)

  const workflow = unique('record')
  const id = await createWorkflow(page, workflow, [
    { id: 'every', type: 'trigger.schedule', params: { interval: '1h', runOnStart: false } },
    { id: 'record', type: 'data.record', params: { dataset: name } },
    { id: 'read', type: 'data.read', params: { dataset: name, read: 'every row' } },
  ])

  const run = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(run.status(), await run.text()).toBe(200)
  const result = (await run.json()) as {
    status: string
    runs: { nodeId: string; output?: Record<string, number> }[]
  }
  expect(result.status).toBe('succeeded')
  expect(result.runs.find((r) => r.nodeId === 'read')?.output).toEqual({ main: 1 })

  const rows = (await (await page.request.get(`/api/v1/datasets/${name}/rows`)).json()) as {
    rows: { row: Record<string, unknown> }[]
  }
  expect(rows.rows).toHaveLength(1)
  expect(rows.rows[0].row).toMatchObject({ _workflow: id, _mode: 'paper' })

  // In the editor, the dataset is picked from yours, or made there.
  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'record').click()
  const settings = page.getByRole('complementary', { name: 'Settings' })
  const picker = settings.getByRole('combobox', { name: /Dataset/ })
  await expect(picker).toHaveValue(name)

  const fresh = datasetName('bets')
  await picker.selectOption('New dataset…')
  const dialog = page.getByRole('dialog', { name: 'New dataset' })
  await expect(dialog.getByLabel('Keep days')).toHaveValue('90')
  await dialog.getByLabel('Name').fill(fresh)
  await dialog.getByRole('button', { name: 'Make it' }).click()
  await expect(picker).toHaveValue(fresh)

  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()

  expect((await page.request.post(`/api/v1/workflows/${id}/activate`)).status()).toBe(200)

  // Deleting the one it reads names it, and switches it off.
  await page.goto('/data')
  const row = page
    .getByRole('table', { name: 'Datasets' })
    .getByRole('row', { name: new RegExp(name) })
  await row.getByRole('button', { name: `Delete ${name}` }).click()
  const confirm = page.getByRole('alertdialog')
  await expect(confirm).toContainText(`“${workflow}” (on) uses it: those on are switched off.`)
  await confirm.getByRole('button', { name: 'Delete' }).click()
  await expect(row).toHaveCount(0)

  const after = (await (await page.request.get(`/api/v1/workflows/${id}`)).json()) as {
    active: boolean
  }
  expect(after.active).toBe(false)

  const refused = await page.request.post(`/api/v1/workflows/${id}/activate`)
  expect(refused.status()).toBe(400)
  expect(await refused.text()).toContain(`names the dataset \\"${name}\\", which you do not have`)
})
