import { createWorkflow, expect, test, unique } from './fixtures'

test('code nodes run TypeScript in the sandbox, and saving compiles it', async ({ page }) => {
  const id = await createWorkflow(page, unique('code'), [
    { id: 'manual', type: 'trigger.manual' },
    {
      id: 'make',
      type: 'code.transform',
      params: {
        source: `import { dec } from "@polythought/sdk"
export function run(): { spread: string }[] {
  return ["0.05", "0.01", "0.03"].map((s) => ({ spread: dec(s).toFixed(2) }))
}`,
      },
    },
    { id: 'split', type: 'code.route' },
  ])

  const res = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(res.status()).toBe(200)

  const run = (await res.json()) as {
    status: string
    runs: { nodeId: string; output?: Record<string, number> }[]
  }
  expect(run.status).toBe('succeeded')
  expect(run.runs.find((r) => r.nodeId === 'make')?.output).toEqual({ main: 3 })
  expect(run.runs.find((r) => r.nodeId === 'split')?.output).toEqual({ wide: 2, tight: 1 })

  const refused = await page.request.post('/api/v1/workflows', {
    data: {
      name: unique('code'),
      nodes: [
        { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
        {
          id: 'keep',
          type: 'code.filter',
          params: { source: 'export function keep( {' },
          position: { x: 300, y: 0 },
        },
      ],
      connections: [{ from: 'manual', fromPort: 'main', to: 'keep', toPort: 'main' }],
    },
  })
  expect(refused.status()).toBe(400)
  expect(await refused.text()).toContain('param \\"source\\": line 1, column 24')
})
