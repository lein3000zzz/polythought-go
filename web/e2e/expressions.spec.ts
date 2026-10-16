import { canvasNode, createWorkflow, expect, fillSetting, test, unique } from './fixtures'

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

test('the editor takes expressions in any setting, and saving checks them', async ({ page }) => {
  const id = await createWorkflow(page, unique('expressions'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'markets', type: 'polymarket.markets' },
    { id: 'top', type: 'transform.top', params: { field: 'liquidity' } },
  ])

  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'top').click()
  const settings = page.getByRole('complementary', { name: 'Settings' })

  // A number takes one as typed; a dropdown through its button.
  await fillSetting(settings, 'limit', '{{ params.field.length }}')
  await expect(settings.getByText('Enter a whole number')).toHaveCount(0)

  await settings.getByRole('button', { name: 'Use an expression for Order' }).click()
  await fillSetting(settings, 'order', "{{ 'asc' }}")

  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()

  // One that does not compile is not saved.
  await fillSetting(settings, 'limit', '{{ params.field.length + }}')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/Not saved: the workflow has problems/)).toBeVisible()
  await expect(settings.getByText(/Unexpected end of the expression/)).toBeVisible()
})
