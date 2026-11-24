import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

test('runs of an active workflow appear as they finish', async ({ page }) => {
  // Every 5 seconds, the fastest an instance allows unless its admin says (D435): it runs
  // once as it is switched on, then on each tick.
  const id = await createWorkflow(page, unique('every-5s'), [
    { id: 'every', type: 'trigger.schedule', params: { interval: '5s', runOnStart: true } },
    { id: 'log', type: 'output.log' },
  ])

  await page.goto(`/workflows/${id}`)
  await page.getByLabel('Active').click()
  await expect(page.getByText('Workflow activated')).toBeVisible()

  await page.getByRole('button', { name: 'History' }).click()
  await page.getByLabel('Show new runs as they finish').click()

  // New runs arrive over the event stream; nothing reloads the list.
  const runs = page.getByRole('tabpanel', { name: 'Executions' }).getByRole('listitem')
  await expect(runs.first()).toBeVisible()
  const seen = await runs.count()
  await expect.poll(() => runs.count(), { timeout: 15_000 }).toBeGreaterThanOrEqual(seen + 2)

  await expect(page.getByText(/started by every/)).toBeVisible()
  await expect(canvasNode(page, 'log').getByLabel('Succeeded')).toBeVisible()

  await page.getByLabel('Active').click()
  await expect(page.getByText('Workflow deactivated')).toBeVisible()
})
