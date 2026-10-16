import { createWorkflow, expect, test, unique } from './fixtures'

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
