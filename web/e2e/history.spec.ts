import { createWorkflow, expect, test, unique } from './fixtures'

// The editor's History panel: the runs first, as before, and who switched the workflow
// and why on its second page (D433).
test('the History panel says who switched the workflow on and off', async ({ page }) => {
  const id = await createWorkflow(page, unique('switched'), [
    { id: 'every', type: 'trigger.schedule', params: { interval: '1h', runOnStart: false } },
    { id: 'log', type: 'output.log' },
  ])
  expect((await page.request.post(`/api/v1/workflows/${id}/activate`)).status()).toBe(200)
  expect((await page.request.post(`/api/v1/workflows/${id}/deactivate`)).status()).toBe(200)

  await page.goto(`/workflows/${id}`)
  await page.getByRole('button', { name: 'History' }).click()

  const panel = page.getByRole('region', { name: 'History' })
  await expect(panel.getByRole('tab', { name: 'Executions' })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(panel.getByText('No executions yet.', { exact: false })).toBeVisible()

  await panel.getByRole('tab', { name: 'History' }).click()
  const switches = panel.getByRole('tabpanel', { name: 'History' }).getByRole('listitem')
  await expect(switches).toHaveCount(2)
  await expect(switches.first()).toContainText('Switched off')
  await expect(switches.first()).toContainText('by you')
  await expect(switches.last()).toContainText('Switched on')

  await panel
    .getByRole('group', { name: 'Show' })
    .getByRole('button', { name: 'On and off' })
    .click()
  await expect(panel.getByText('Nothing yet.', { exact: false })).toBeVisible()
  await panel
    .getByRole('group', { name: 'Show' })
    .getByRole('button', { name: 'On and off' })
    .click()
  await expect(switches).toHaveCount(2)
})
