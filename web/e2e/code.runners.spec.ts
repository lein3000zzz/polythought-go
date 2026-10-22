import { createWorkflow, expect, test, unique } from './fixtures'

// While the server restarts, requests fail for a moment: the proxy answers 502 and the
// live event stream drops until it reconnects.
test.use({ expectedConsoleError: /status of 50\d|net::ERR_/ })

test('user code runs in runner processes once an admin turns them on, sandboxed (D352, D357)', async ({
  page,
}) => {
  await page.goto('/admin/config/code')
  const code = page.locator('section', { has: page.getByRole('heading', { name: 'Code' }) })
  await expect(code.getByText("Code runs in the server's process.")).toBeVisible()

  // Runner processes apply when the server restarts.
  const restart = async () => {
    await expect(page.getByText('A restart is needed')).toBeVisible()
    await page.getByRole('button', { name: 'Restart now' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Restart' }).click()
    await expect(page.getByText('Server restarted')).toBeVisible({ timeout: 60_000 })
    await page.reload()
  }

  await page.getByLabel('Runner processes').click()
  await page.getByRole('button', { name: 'Save' }).click()
  await restart()

  // In the stack's container, under the sandbox (D337).
  await expect(code.getByText(/^Code runs in \d+ runner processes, sandboxed\.$/)).toBeVisible()

  const id = await createWorkflow(page, unique('runners'), [
    { id: 'manual', type: 'trigger.manual' },
    {
      id: 'make',
      type: 'code.transform',
      params: {
        source: `import { dec } from "@polythought/sdk"
export function run(): { sum: string }[] {
  return [{ sum: dec("0.1").plus("0.2").toFixed(1) }]
}`,
      },
    },
  ])

  const res = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(res.status(), await res.text()).toBe(200)

  const run = (await res.json()) as {
    status: string
    runs: { nodeId: string; samples?: Record<string, { json?: Record<string, unknown> }[]> }[]
  }
  expect(run.status).toBe('succeeded')
  expect(run.runs.find((r) => r.nodeId === 'make')?.samples?.main?.[0]?.json).toEqual({
    sum: '0.3',
  })

  // Back to its default, the server's process, for the other tests.
  const reset = await page.request.patch('/api/v1/admin/settings', {
    data: { values: { 'code.runners': null } },
  })
  expect(reset.status(), await reset.text()).toBe(200)
  await page.reload()
  await restart()
  await expect(code.getByText("Code runs in the server's process.")).toBeVisible()
})
