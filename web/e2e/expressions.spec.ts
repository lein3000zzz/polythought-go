import { createWorkflow, expect, test, unique } from './fixtures'

test('settings work their expressions out for each run, in the sandbox', async ({ page }) => {
  const run = async (message: string) => {
    const id = await createWorkflow(page, unique('expressions'), [
      { id: 'manual', type: 'trigger.manual' },
      { id: 'log', type: 'output.log', params: { message } },
    ])
    const res = await page.request.post(`/api/v1/workflows/${id}/run`)
    expect(res.status()).toBe(200)

    return (await res.json()) as { status: string; error?: string }
  }

  const worked = await run(
    'run {{ execution.id.slice(0, 8) }} of {{ new Date(execution.startedAt).getUTCFullYear() > 2000 }}, {{ dec("0.1").plus("0.2") }}',
  )
  expect(worked.status).toBe('succeeded')

  const threw = await run('{{ nope() }}')
  expect(threw.status).toBe('failed')
  expect(threw.error).toContain('ReferenceError')
})
