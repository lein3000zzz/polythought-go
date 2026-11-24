import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

// The weather nodes are built and saved here; running them would call Open-Meteo and
// aviationweather.gov, which the tests never do (D252).
test('the weather nodes take a city from the preset, and are in the node list (D441)', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('weather'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'forecast', type: 'weather.forecast', params: { city: 'NYC', models: ['gfs', 'ecmwf'] } },
    {
      id: 'reading',
      type: 'weather.airport',
      params: { station: 'KLGA', zone: 'America/New_York' },
    },
  ])

  // A city that is none, or a zone that is none, is refused as it is saved.
  for (const [params, why] of [
    [{ city: 'Atlantis' }, 'Atlantis'],
    [{ station: 'KLGA', zone: 'Mars/Base' }, '"Mars/Base" is not a time zone'],
  ] as const) {
    const res = await page.request.post('/api/v1/workflows', {
      data: {
        name: unique('weather'),
        nodes: [
          { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
          { id: 'reading', type: 'weather.airport', params, position: { x: 300, y: 0 } },
        ],
        connections: [{ from: 'manual', fromPort: 'main', to: 'reading', toPort: 'main' }],
      },
    })
    expect(res.status(), JSON.stringify(params)).toBe(400)
    expect(((await res.json()) as { error: { message: string } }).error.message).toContain(why)
  }

  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'forecast').click()
  const panel = page.getByRole('complementary', { name: 'Settings' })
  await expect(panel.getByText('Weather forecast', { exact: true })).toBeVisible()
  await expect(panel.getByRole('combobox', { name: 'City' })).toHaveValue('NYC')

  await page.getByLabel('Search nodes').fill('temperature')
  await expect(page.getByRole('button', { name: /Weather forecast/ })).toBeVisible()
  await expect(page.getByRole('button', { name: /Airport reading/ })).toBeVisible()
})
