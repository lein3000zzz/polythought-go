import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

test('an execution waits, then carries on by itself', async ({ page }) => {
  const id = await createWorkflow(page, unique('waits'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'wait', type: 'logic.wait', params: { for: '3s' } },
    { id: 'log', type: 'output.log' },
  ])

  await page.goto(`/workflows/${id}`)
  await page.getByRole('button', { name: 'Run', exact: true }).click()

  await expect(page.getByText(/Waiting at wait until/)).toBeVisible()
  await expect(canvasNode(page, 'wait').getByLabel('Waiting')).toBeVisible()

  // The server's timer carries it on; the live events bring the result.
  await expect(canvasNode(page, 'log').getByLabel('Succeeded')).toBeVisible({ timeout: 15_000 })
  await expect(canvasNode(page, 'wait').getByLabel('Succeeded')).toBeVisible()
  await expect(page.getByText(/Waiting at wait/)).toHaveCount(0)
})

test('a waiting execution can be cancelled', async ({ page }) => {
  const id = await createWorkflow(page, unique('cancel'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'wait', type: 'logic.wait', params: { for: '1h' } },
    { id: 'log', type: 'output.log' },
  ])

  await page.goto(`/workflows/${id}`)
  await page.getByRole('button', { name: 'Run', exact: true }).click()
  await expect(page.getByText(/Waiting at wait until/)).toBeVisible()

  await page.getByRole('button', { name: 'Cancel execution' }).click()
  await expect(page.getByText('Execution cancelled')).toBeVisible()
  await expect(canvasNode(page, 'wait').getByLabel('Cancelled while it waited')).toBeVisible()
  await expect(page.getByText('cancelled by you').first()).toBeVisible()
})

test.describe('across a restart', () => {
  // The server is down for a moment while it restarts.
  test.use({ expectedConsoleError: /status of 50\d|net::ERR_/ })

  test('a waiting execution carries on after the server restarts', async ({ page }) => {
    const id = await createWorkflow(page, unique('restart'), [
      { id: 'manual', type: 'trigger.manual' },
      { id: 'wait', type: 'logic.wait', params: { for: '5s' } },
      { id: 'log', type: 'output.log' },
    ])

    const ran = await page.request.post(`/api/v1/workflows/${id}/run`)
    expect(ran.status()).toBe(200)
    const execution = (await ran.json()) as { id: string; status: string }
    expect(execution.status).toBe('waiting')

    expect((await page.request.post('/api/v1/admin/restart')).status()).toBe(202)

    // Back up, it finds the wait in the database and carries the execution on.
    await expect
      .poll(
        async () => {
          const res = await page.request.get(`/api/v1/workflows/${id}/executions/${execution.id}`)
          return res.ok() ? ((await res.json()) as { status: string }).status : 'down'
        },
        { timeout: 30_000, intervals: [1_000] },
      )
      .toBe('succeeded')
  })
})
