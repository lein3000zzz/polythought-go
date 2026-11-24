import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

type Hook = { url: string; enabled: boolean; listening: boolean }
type Execution = {
  status: string
  error?: string
  triggeredBy: string
  runs: { nodeId: string; output?: Record<string, number> }[]
}

test('a service calls a workflow’s secret address, each call a run, once an admin allows it (D439)', async ({
  page,
}) => {
  const settings = (values: Record<string, unknown>) =>
    page.request.patch('/api/v1/admin/settings', { data: { values } })

  // The call's body read as JSON: the If sends it on as true.
  const id = await createWorkflow(page, unique('hooked'), [
    { id: 'hook', type: 'trigger.webhook' },
    {
      id: 'warm',
      type: 'logic.if',
      params: { field: 'body.temperature', operator: 'gt', value: '70' },
    },
  ])

  const hook = async () => {
    const res = await page.request.get(`/api/v1/workflows/${id}/hooks/hook`)
    expect(res.status(), await res.text()).toBe(200)

    return (await res.json()) as Hook
  }

  // Off on a new instance: the address answers 503.
  const first = await hook()
  expect(first.url).toMatch(/^http:\/\/localhost:3100\/hooks\/[\w-]{43}$/)
  expect(first.enabled).toBe(false)
  expect((await page.request.post(first.url, { data: {} })).status()).toBe(503)

  try {
    const allow = await settings({ 'workflows.inbound_webhooks': true })
    expect(allow.status(), await allow.text()).toBe(200)

    // Not taking calls while the workflow is off.
    expect((await page.request.post(first.url, { data: {} })).status()).toBe(409)

    expect((await page.request.post(`/api/v1/workflows/${id}/activate`)).status()).toBe(200)

    // Its owner sees it in the editor, taking calls.
    await page.goto(`/workflows/${id}`)
    await canvasNode(page, 'hook').click()
    const panel = page.getByRole('complementary', { name: 'Settings' })
    await expect(panel.getByRole('textbox', { name: 'Address' })).toHaveValue(first.url)
    await expect(panel.getByText(/^Taking calls/)).toBeVisible()

    // A call, answered at once with its run's ID; the run takes it.
    let executionId = ''
    await expect
      .poll(async () => {
        const res = await page.request.post(`${first.url}?city=nyc`, {
          data: { temperature: 71.5, station: 'KLGA' },
        })
        if (res.status() === 202)
          executionId = ((await res.json()) as { executionId: string }).executionId

        return res.status()
      })
      .toBe(202)

    let run: Execution | undefined
    await expect
      .poll(async () => {
        const res = await page.request.get(`/api/v1/workflows/${id}/executions/${executionId}`)
        run = res.ok() ? ((await res.json()) as Execution) : undefined

        return run?.status
      })
      .toBe('succeeded')
    expect(run?.triggeredBy).toBe('hook')
    expect(run?.runs.find((r) => r.nodeId === 'warm')?.output).toEqual({ true: 1 })

    // Renewed: the old address answers no more, the new one does.
    await panel.getByRole('button', { name: 'Renew the address' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Renew' }).click()
    await expect(panel.getByRole('textbox', { name: 'Address' })).not.toHaveValue(first.url)

    const renewed = await hook()
    expect(renewed.url).not.toBe(first.url)
    expect((await page.request.post(first.url, { data: {} })).status()).toBe(404)
    expect((await page.request.post(renewed.url, { data: 'plain text' })).status()).toBe(202)
  } finally {
    await page.request.post(`/api/v1/workflows/${id}/deactivate`)

    // Back to the default, for the other tests.
    const reset = await settings({ 'workflows.inbound_webhooks': null })
    expect(reset.status(), await reset.text()).toBe(200)
  }
})
