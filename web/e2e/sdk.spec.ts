import { expect, test } from './fixtures'

// The server serves the Python SDK of its own version, as pip reads a page of links:
// pip install polythought --find-links <instance>/api/v1/sdk/python/.
test('the server serves the Python SDK for pip', async ({ request }) => {
  const index = await request.get('/api/v1/sdk/python/')
  expect(index.status(), await index.text()).toBe(200)

  const wheel = /href="([^"]+\.whl)"/.exec(await index.text())?.[1] ?? ''
  expect(wheel).toMatch(/^polythought-[0-9][^/]*-py3-none-any\.whl$/)

  const got = await request.get(`/api/v1/sdk/python/${wheel}`)
  expect(got.status()).toBe(200)

  const body = await got.body()
  expect(body.subarray(0, 2).toString()).toBe('PK') // a zip
  expect(body.includes(Buffer.from('polythought/serve.py'))).toBe(true)
})
