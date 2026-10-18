import { createWorkflow, expect, test, unique } from './fixtures'

type Books = {
  orders: { status: string; side: string; fills: { shares: string; fee: string }[] }[]
}

test('a paper order fills against the live book, and the workflow keeps its books', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('paper'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'markets', type: 'polymarket.markets', params: { limit: 1 } },
    {
      id: 'buy',
      type: 'trading.placeOrder',
      params: {
        from: 'settings',
        tokenId: '{{ outcomes[0].tokenId }}',
        side: 'buy',
        price: '0.99',
        shares: '{{ minOrderSize }}',
      },
    },
  ])

  const before = (await (
    await page.request.get(`/api/v1/workflows/${id}/paper-account`)
  ).json()) as {
    open: boolean
    cash: string
  }
  expect(before).toEqual(expect.objectContaining({ open: false, cash: '1000' }))

  const res = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(res.status()).toBe(200)
  expect(((await res.json()) as { status: string }).status).toBe('succeeded')

  // The market's least order, at up to 0.99: the book's best asks fill it.
  const books = (await (await page.request.get(`/api/v1/workflows/${id}/orders`)).json()) as Books
  expect(books.orders).toHaveLength(1)
  expect(['filled', 'partial']).toContain(books.orders[0].status)
  expect(books.orders[0].fills.length).toBeGreaterThan(0)

  const after = (await (
    await page.request.get(`/api/v1/workflows/${id}/paper-account`)
  ).json()) as {
    open: boolean
    cash: string
  }
  expect(after.open).toBe(true)
  expect(Number(after.cash)).toBeLessThan(1000)

  const held = (await (await page.request.get(`/api/v1/workflows/${id}/holdings`)).json()) as {
    holdings: { shares: string; cost: string }[]
  }
  expect(held.holdings).toHaveLength(1)
  expect(Number(held.holdings[0].shares)).toBeGreaterThan(0)
  expect(Number(held.holdings[0].cost)).toBeCloseTo(1000 - Number(after.cash), 6)

  // The trading nodes are in the node list.
  await page.goto(`/workflows/${id}`)
  const list = page.getByRole('navigation', { name: 'Node types' })
  await expect(list.getByText('Copy sizing', { exact: true })).toBeVisible()
  await expect(list.getByText('Place order', { exact: true })).toBeVisible()
})

test('the risk guard makes a copy over its cap smaller before it is placed', async ({ page }) => {
  const id = await createWorkflow(page, unique('guarded'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'markets', type: 'polymarket.markets', params: { limit: 1 } },
    {
      // A copy as Copy sizing makes one, of $50.
      id: 'copy',
      type: 'code.transform',
      params: {
        source: `export function run(items: any[]) {
  return items.map((m) => ({
    copy: { side: "buy", tokenId: m.outcomes[0].tokenId, marketId: m.id, price: "0.99", dollars: "50", shares: null, type: "FAK" },
  }))
}`,
      },
    },
    { id: 'guard', type: 'trading.riskGuard', params: { maxPerCopy: '20' } },
    { id: 'buy', type: 'trading.placeOrder' },
  ])

  const res = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(res.status()).toBe(200)
  expect(((await res.json()) as { status: string }).status).toBe('succeeded')

  const books = (await (await page.request.get(`/api/v1/workflows/${id}/orders`)).json()) as {
    orders: { status: string; dollars: string; fills: { dollars: string }[] }[]
  }
  expect(books.orders).toHaveLength(1)
  expect(books.orders[0].dollars).toBe('20')
  expect(['filled', 'partial']).toContain(books.orders[0].status)

  const spent = books.orders[0].fills.reduce((sum, f) => sum + Number(f.dollars), 0)
  expect(spent).toBeGreaterThan(0)
  expect(spent).toBeLessThanOrEqual(20)
})
