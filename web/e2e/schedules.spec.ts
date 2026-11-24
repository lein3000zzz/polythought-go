import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

type Run = {
  status: string
  error?: string
  runs: { nodeId: string; samples?: Record<string, { json?: Record<string, unknown> }[]> }[]
}

test('a schedule at times of day in a time zone is checked as it is saved, and runs by hand (D440)', async ({
  page,
}) => {
  const node = (params: Record<string, unknown>) => ({
    name: unique('at times'),
    nodes: [{ id: 'daily', type: 'trigger.schedule', params, position: { x: 0, y: 0 } }],
    connections: [],
  })

  // Saving says what is wrong with it.
  for (const [params, why] of [
    [{ mode: 'at' }, 'missing param "times"'],
    [{ mode: 'at', times: ['25:00'] }, '"25:00" is not a time of day'],
    [{ mode: 'at', times: ['06:30'], zone: 'Mars/Olympus' }, '"Mars/Olympus" is not a time zone'],
    [{ mode: 'every' }, 'missing param "interval"'],
  ] as const) {
    const res = await page.request.post('/api/v1/workflows', { data: node(params) })
    expect(res.status(), JSON.stringify(params)).toBe(400)
    const body = (await res.json()) as { error: { message: string } }
    expect(body.error.message).toContain(why)
  }

  const id = await createWorkflow(page, unique('at times'), [
    {
      id: 'daily',
      type: 'trigger.schedule',
      params: {
        mode: 'at',
        times: ['06:30', '18:00'],
        days: ['mon', 'fri'],
        zone: 'America/Chicago',
      },
    },
    { id: 'log', type: 'output.log' },
  ])

  // By hand, as if its time had come.
  const res = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(res.status(), await res.text()).toBe(200)
  const run = (await res.json()) as Run
  expect(run.status, run.error).toBe('succeeded')
  expect(run.runs.find((r) => r.nodeId === 'daily')?.samples?.main?.[0]?.json).toMatchObject({
    missed: false,
  })

  // The editor ticks its days.
  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'daily').click()
  const panel = page.getByRole('complementary', { name: 'Settings' })
  await expect(panel.getByRole('checkbox', { name: 'mon' })).toBeChecked()
  await expect(panel.getByRole('checkbox', { name: 'tue' })).not.toBeChecked()
})
