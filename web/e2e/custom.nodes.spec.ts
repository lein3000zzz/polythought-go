import { createWorkflow, expect, signedOut, signIn, test, unique } from './fixtures'

type Execution = {
  status: string
  error?: string
  runs: { nodeId: string; output?: Record<string, number> }[]
}

test('a custom node runs in its owner workflows, and warns before a change breaks them', async ({
  page,
}) => {
  const name = unique('split')
  const definition = {
    name,
    label: 'Spread split',
    settings: [{ name: 'threshold', kind: 'decimal', default: '0.02' }],
    outputs: ['wide', 'tight'],
    items: { passed: true, fields: [] },
    source: `import { dec } from "@polythought/sdk"
import type { Context } from "@polythought/sdk"

export function run(_items: unknown[], ctx: Context<{ threshold: string }>) {
  const books = ["0.05", "0.01", "0.04"].map((spread) => ({ spread }))
  const wide = books.filter((b) => dec(b.spread).gt(ctx.params.threshold))
  return { wide, tight: books.filter((b) => !wide.includes(b)) }
}`,
  }

  const created = await page.request.post('/api/v1/custom-nodes', { data: definition })
  expect(created.status(), await created.text()).toBe(201)
  const node = (await created.json()) as { id: string; type: string; version: number }
  expect(node.type).toBe(`custom.${name}`)

  // It is one of the owner's node types now.
  const types = (await (await page.request.get('/api/v1/node-types')).json()) as {
    nodeTypes: { name: string; code: boolean; outputs: string[] }[]
  }
  expect(types.nodeTypes.find((t) => t.name === node.type)).toMatchObject({
    code: true,
    outputs: ['wide', 'tight'],
  })

  const id = await createWorkflow(page, unique('custom'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'split', type: node.type, params: { threshold: '0.03' } },
  ])

  const run = async () => {
    const res = await page.request.post(`/api/v1/workflows/${id}/run`)
    return { status: res.status(), body: (await res.json()) as Execution }
  }

  const first = await run()
  expect(first.body.status).toBe('succeeded')
  expect(first.body.runs.find((r) => r.nodeId === 'split')?.output).toEqual({ wide: 2, tight: 1 })

  // Taking away the setting the workflow sets breaks it: asked first.
  const edit = (confirm: boolean) =>
    page.request.put(`/api/v1/custom-nodes/${node.id}`, {
      data: { ...definition, settings: [], version: node.version, confirm },
    })

  const warned = await edit(false)
  expect(warned.status()).toBe(409)
  const warning = (await warned.json()) as {
    error: { code: string; workflows: { id: string; problems: { message: string }[] }[] }
  }
  expect(warning.error.code).toBe('breaks_workflows')
  expect(warning.error.workflows).toHaveLength(1)
  expect(warning.error.workflows[0].id).toBe(id)
  expect(warning.error.workflows[0].problems[0].message).toBe(
    'node "split": unknown param "threshold"',
  )

  expect((await edit(true)).status()).toBe(200)

  // Run by hand, it says what to fix, as saving would.
  const broken = await run()
  expect(broken.status).toBe(400)
  expect(JSON.stringify(broken.body)).toContain('unknown param \\"threshold\\"')

  // Deleting it while the workflow uses it is asked first too.
  const refused = await page.request.delete(`/api/v1/custom-nodes/${node.id}`)
  expect(refused.status()).toBe(409)
  expect((await page.request.delete(`/api/v1/custom-nodes/${node.id}?confirm=true`)).status()).toBe(
    204,
  )
  expect((await page.request.get(`/api/v1/custom-nodes/${node.id}`)).status()).toBe(404)
})

test('a node made on its page is in the node list, and the editor marks what a change breaks', async ({
  page,
}) => {
  const name = unique('pass')
  const label = `Pass ${name}`

  await page.goto('/nodes')
  await page.getByRole('link', { name: 'New custom node' }).click()
  await page.getByRole('textbox', { name: 'Name' }).fill(name)
  await page.getByRole('textbox', { name: 'Label' }).fill(label)
  // The starter passes the items on as it is: it works as made.
  await page.getByRole('button', { name: 'Make the node' }).click()
  await expect(page.getByText(`Made custom.${name}`)).toBeVisible()
  await expect(page).toHaveURL(/\/nodes\/[0-9a-f-]+$/)
  const nodeUrl = page.url()

  const id = await createWorkflow(page, unique('custom'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'pass', type: `custom.${name}` },
    { id: 'log', type: 'output.log' },
  ])

  const run = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(((await run.json()) as { status: string }).status).toBe('succeeded')

  await page.goto(`/workflows/${id}`)
  const list = page.getByRole('navigation', { name: 'Node types' })
  await expect(list.getByRole('button', { name: new RegExp(label) })).toBeVisible()
  await expect(list.getByRole('link', { name: /New custom node/ })).toBeVisible()
  await expect(page.getByText(/no longer matches its node types/)).toBeHidden()

  // Renaming its output breaks the connection to Log: asked first.
  await page.goto(nodeUrl)
  await page.getByRole('textbox', { name: 'Output 1' }).fill('out')
  await page.getByRole('button', { name: 'Save' }).click()
  const warning = page.getByRole('alertdialog', { name: 'This breaks a workflow' })
  await expect(warning.getByText(`has no output port "main"`)).toBeVisible()
  await warning.getByRole('button', { name: 'Save anyway' }).click()
  await expect(page.getByText('Saved', { exact: true })).toBeVisible()

  await page.goto(`/workflows/${id}`)
  await expect(page.getByText(/no longer matches its node types/)).toBeVisible()
})

test('sharing a workflow brings its custom nodes; using it copies them, renamed if need be', async ({
  page,
  browser,
}) => {
  const name = unique('shared')
  const definition = (source: string) => ({
    name,
    label: 'Shared pass',
    settings: [],
    outputs: ['main'],
    items: { passed: true, fields: [] },
    source,
  })
  const passing = 'export function run(items: unknown[]) { return items }'

  expect(
    (await page.request.post('/api/v1/custom-nodes', { data: definition(passing) })).status(),
  ).toBe(201)

  const id = await createWorkflow(page, unique('brings'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'pass', type: `custom.${name}` },
  ])
  const shared = await page.request.put(`/api/v1/workflows/${id}/share`, {
    data: { summary: 'Brings a node', description: '' },
  })
  expect(shared.status(), await shared.text()).toBe(200)
  const template = (await shared.json()) as { id: string }

  // Anyone reads its code before using it (D62).
  await page.goto(`/library/${template.id}`)
  const brought = page.getByRole('region', { name: 'Custom nodes it brings' })
  await expect(brought.getByText(`custom.${name}`)).toBeVisible()
  await brought.getByText('Shared pass').click()
  await expect(brought.getByLabel('Code of Shared pass')).toContainText('return items')

  // Someone else, with a different node of that name, uses it.
  const person = { email: `${unique('user')}@e2e.test`, password: 'user-password-123' }
  expect((await page.request.post('/api/v1/admin/users', { data: person })).status()).toBe(201)

  const theirs = await browser.newContext({ storageState: signedOut })
  const them = await theirs.newPage()
  await signIn(them, person)

  const mine = 'export function run() { return [] }'
  expect(
    (await them.request.post('/api/v1/custom-nodes', { data: definition(mine) })).status(),
  ).toBe(201)

  const used = await them.request.post(`/api/v1/library/${template.id}/use`, { data: {} })
  expect(used.status(), await used.text()).toBe(201)
  const workflow = (await used.json()) as { id: string; nodes: { id: string; type: string }[] }
  expect(workflow.nodes.find((n) => n.id === 'pass')?.type).toBe(`custom.${name}-2`)

  const list = (await (await them.request.get('/api/v1/custom-nodes')).json()) as {
    customNodes: { name: string; source: string }[]
  }
  expect(list.customNodes.map((n) => [n.name, n.source])).toEqual([
    [name, mine],
    [`${name}-2`, passing],
  ])

  const run = await them.request.post(`/api/v1/workflows/${workflow.id}/run`)
  expect(((await run.json()) as { status: string }).status).toBe('succeeded')

  // Using it again makes no more copies: the one they have is the same.
  const again = await them.request.post(`/api/v1/library/${template.id}/use`, { data: {} })
  const second = (await again.json()) as { nodes: { id: string; type: string }[] }
  expect(second.nodes.find((n) => n.id === 'pass')?.type).toBe(`custom.${name}-2`)
  const after = (await (await them.request.get('/api/v1/custom-nodes')).json()) as {
    customNodes: unknown[]
  }
  expect(after.customNodes).toHaveLength(2)

  await theirs.close()
})
