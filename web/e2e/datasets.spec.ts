import { expect, test, unique } from './fixtures'

// A notebook's datasets: made, appended to and replaced at once over the API with a token
// that may write them, then seen and deleted on the Data page.
test('a notebook publishes a dataset, and it shows on the Data page', async ({
  page,
  playwright,
  baseURL,
}) => {
  const made = await page.request.post('/api/v1/tokens', {
    data: { name: unique('notebook'), scopes: ['data'] },
  })
  expect(made.status(), await made.text()).toBe(201)
  const token = ((await made.json()) as { token: string }).token

  const api = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  })
  const name = unique('forecasts')
    .toLowerCase()
    .replace(/[^a-z0-9_-]/g, '-')

  try {
    const created = await api.post('/api/v1/datasets', { data: { name, keyField: 'city' } })
    expect(created.status(), await created.text()).toBe(201)

    const appended = await api.post(`/api/v1/datasets/${name}/rows`, {
      data: {
        rows: [
          { city: 'NYC', high: 51.25 },
          { city: 'LDN', high: 9 },
        ],
      },
    })
    expect(appended.status(), await appended.text()).toBe(200)
    expect(((await appended.json()) as { rows: number }).rows).toBe(2)

    // A row by its key, its number as written.
    const nyc = await api.get(`/api/v1/datasets/${name}/rows/NYC`)
    expect(nyc.status(), await nyc.text()).toBe(200)
    expect(await nyc.text()).toContain('"high":51.25')

    // Replaced at once: until the finish, the old rows stand.
    const stage = (
      (await (await api.post(`/api/v1/datasets/${name}/replace`)).json()) as { stage: string }
    ).stage
    const staged = await api.post(`/api/v1/datasets/${name}/replace/${stage}/rows`, {
      data: { rows: [{ city: 'TKY', high: 14 }] },
    })
    expect(staged.status(), await staged.text()).toBe(200)

    const before = (await (await api.get(`/api/v1/datasets/${name}`)).json()) as { rows: number }
    expect(before.rows).toBe(2)

    const finished = await api.post(`/api/v1/datasets/${name}/replace/${stage}/finish`)
    expect(finished.status(), await finished.text()).toBe(200)
    expect(((await finished.json()) as { rows: number }).rows).toBe(1)

    const csv = await api.get(`/api/v1/datasets/${name}/csv`)
    expect(await csv.text()).toBe('city,high\nTKY,14\n')

    // A row that is no object is refused, saying why.
    const bad = await api.post(`/api/v1/datasets/${name}/rows`, { data: { rows: [[1, 2]] } })
    expect(bad.status()).toBe(400)

    // Its owner sees it, and deletes it.
    await page.goto('/data')
    const row = page
      .getByRole('table', { name: 'Datasets' })
      .getByRole('row', { name: new RegExp(name) })
    await expect(row).toContainText('city')
    await row.getByRole('button', { name: name, exact: true }).click()
    await expect(page.getByRole('region', { name: `${name}'s first rows` })).toContainText('TKY')

    await row.getByRole('button', { name: `Delete ${name}` }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
    await expect(row).toHaveCount(0)
  } finally {
    await api.dispose()
  }
})
