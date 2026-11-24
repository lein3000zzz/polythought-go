import { createWorkflow, expect, test, unique } from './fixtures'

type Run = {
  status: string
  error?: string
  runs: { nodeId: string; samples?: Record<string, { json?: Record<string, unknown> }[]> }[]
}

test('an HTTP request reads an API inside the network once an admin allows it (D438)', async ({
  page,
}) => {
  const settings = (values: Record<string, unknown>) =>
    page.request.patch('/api/v1/admin/settings', { data: { values } })

  // Mailpit, in the e2e stack, answers JSON on its API inside the network.
  const id = await createWorkflow(page, unique('http request'), [
    { id: 'manual', type: 'trigger.manual' },
    {
      id: 'ask',
      type: 'http.request',
      params: {
        url: 'http://mailpit:8025/api/v1/info',
        query: ['run={{ execution.id }}'],
        headers: ['Accept: application/json'],
      },
    },
  ])

  const run = async () => {
    const res = await page.request.post(`/api/v1/workflows/${id}/run`)
    expect(res.status(), await res.text()).toBe(200)

    return (await res.json()) as Run
  }

  // Refused to begin with: it is inside the server's network.
  const refused = await run()
  expect(refused.status).toBe('failed')
  expect(refused.error).toContain("address inside the server's network")

  try {
    const allow = await settings({ 'webhook.allow_private': true })
    expect(allow.status(), await allow.text()).toBe(200)

    const answered = await run()
    expect(answered.status, answered.error).toBe('succeeded')

    const response = answered.runs.find((r) => r.nodeId === 'ask')?.samples?.main?.[0]?.json
      ?.response as {
      status: number
      headers: Record<string, string>
      body: Record<string, unknown>
    }
    expect(response.status).toBe(200)
    expect(response.headers['content-type']).toContain('application/json')
    expect(response.body).toHaveProperty('Version')
  } finally {
    // Back to the default, for the other tests.
    const reset = await settings({ 'webhook.allow_private': null })
    expect(reset.status(), await reset.text()).toBe(200)
  }
})
