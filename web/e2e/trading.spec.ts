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

  // Its results, in the Trading view (D104).
  await page.getByRole('tab', { name: 'Trading' }).click()
  const account = page.getByRole('region', { name: 'Paper account' })
  await expect(account.getByText('Worth')).toBeVisible()
  await expect(
    page.getByRole('region', { name: 'Traders' }).getByRole('row', { name: /Copied from no one/ }),
  ).toContainText('1')
  await expect(page.getByRole('region', { name: 'Holdings' }).getByRole('row')).toHaveCount(2)
  await expect(
    page.getByRole('region', { name: 'Orders' }).getByText(/^filled|^partial/),
  ).toBeVisible()

  // Reset starts it over, the orders kept (D107).
  await account.getByRole('button', { name: 'Reset' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Reset' }).click()
  await expect(account.getByText('$1,000.00').first()).toBeVisible()
  await expect(page.getByText('Nothing held.')).toBeVisible()
  await expect(
    page.getByRole('region', { name: 'Orders' }).getByText(/^Before the reset on/),
  ).toBeVisible()
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

test('a paper GTC under the book rests, holding its cash, until cancelled (D206, D210)', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('resting'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'markets', type: 'polymarket.markets', params: { limit: 1 } },
    {
      id: 'buy',
      type: 'trading.placeOrder',
      params: {
        from: 'settings',
        tokenId: '{{ outcomes[0].tokenId }}',
        side: 'buy',
        price: '{{ tickSize }}', // the lowest price there is: nobody sells there
        shares: '{{ minOrderSize }}',
        type: 'GTC',
      },
    },
  ])

  const res = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(res.status()).toBe(200)
  expect(((await res.json()) as { status: string }).status).toBe('succeeded')

  type Orders = { orders: { id: string; status: string; reason?: string }[] }
  const orders = async () =>
    ((await (await page.request.get(`/api/v1/workflows/${id}/orders`)).json()) as Orders).orders
  const held = async () =>
    (
      (await (await page.request.get(`/api/v1/workflows/${id}/trading`)).json()) as {
        account: { held: string; cash: string }
      }
    ).account

  const placed = await orders()
  expect(placed).toHaveLength(1)
  expect(placed[0].status).toBe('open')
  expect(placed[0].reason).toMatch(/^rests on the book at /)

  const before = await held()
  expect(Number(before.held)).toBeGreaterThan(0)
  expect(before.cash).toBe('1000')

  // Cancelled from the Trading view, its cash is free again.
  await page.goto(`/workflows/${id}`)
  await page.getByRole('tab', { name: 'Trading' }).click()
  await expect(
    page.getByRole('region', { name: 'Paper account' }).getByText('Held by resting orders'),
  ).toBeVisible()
  await page
    .getByRole('region', { name: 'Orders' })
    .getByRole('button', { name: /^Cancel buy/ })
    .click()
  await expect(page.getByText('Order cancelled')).toBeVisible()

  expect((await orders())[0].status).toBe('canceled')
  expect((await held()).held).toBe('0')
})

test('Cancel orders cancels the resting order its run placed, and frees its cash (D207)', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('cancel'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'markets', type: 'polymarket.markets', params: { limit: 1 } },
    {
      id: 'buy',
      type: 'trading.placeOrder',
      params: {
        from: 'settings',
        tokenId: '{{ outcomes[0].tokenId }}',
        side: 'buy',
        price: '{{ tickSize }}',
        shares: '{{ minOrderSize }}',
        type: 'GTC',
      },
    },
  ])

  // A resting order leaves Place order on its resting port: Cancel orders goes there.
  const workflow = (await (await page.request.get(`/api/v1/workflows/${id}`)).json()) as {
    name: string
    version: number
    nodes: object[]
    connections: object[]
  }
  const saved = await page.request.put(`/api/v1/workflows/${id}`, {
    data: {
      name: workflow.name,
      version: workflow.version,
      nodes: [
        ...workflow.nodes,
        {
          id: 'cancel',
          type: 'trading.cancelOrders',
          params: { which: 'order' },
          position: { x: 900, y: 0 },
        },
      ],
      connections: [
        ...workflow.connections,
        { from: 'buy', fromPort: 'resting', to: 'cancel', toPort: 'main' },
      ],
    },
  })
  expect(saved.status(), await saved.text()).toBe(200)

  const res = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(res.status()).toBe(200)
  const run = (await res.json()) as {
    status: string
    runs: {
      nodeId: string
      output?: Record<string, number>
      samples?: Record<string, { json?: { cancel?: { cancelled: { status: string }[] } } }[]>
    }[]
  }
  expect(run.status).toBe('succeeded')

  const cancel = run.runs.find((r) => r.nodeId === 'cancel')
  expect(cancel?.output).toEqual({ main: 1 })
  expect(cancel?.samples?.main[0].json?.cancel?.cancelled.map((o) => o.status)).toEqual([
    'canceled',
  ])

  const orders = (
    (await (await page.request.get(`/api/v1/workflows/${id}/orders`)).json()) as {
      orders: { status: string }[]
    }
  ).orders
  expect(orders.map((o) => o.status)).toEqual(['canceled'])

  const account = (
    (await (await page.request.get(`/api/v1/workflows/${id}/trading`)).json()) as {
      account: { held: string; cash: string }
    }
  ).account
  expect(account.held).toBe('0')
  expect(account.cash).toBe('1000')
})

test('the live orders page lists none on paper, and downloads them as CSV (D228)', async ({
  page,
}) => {
  await page.goto('/workflows')
  await page.getByRole('link', { name: 'Live orders' }).click()

  await expect(page.getByRole('heading', { name: 'Live orders' })).toBeVisible()
  await expect(page.getByText('No live orders yet.')).toBeVisible()

  await page.getByLabel('Status').selectOption('refused')
  await expect(page).toHaveURL(/\/orders\?status=refused$/)
  await expect(page.getByText('No live orders like that.')).toBeVisible()
  await expect(page.getByRole('link', { name: 'Download CSV' })).toHaveAttribute(
    'href',
    '/api/v1/orders/export?status=refused',
  )

  const csv = await page.request.get('/api/v1/orders/export?status=refused')
  expect(csv.status()).toBe(200)
  expect(csv.headers()['content-type']).toBe('text/csv; charset=utf-8')
  expect((await csv.text()).trim()).toBe(
    'at,workflow,workflow_deleted,wallet_id,market,outcome,token_id,side,type,post_only,price,shares,' +
      'dollars,status,filled_shares,filled_dollars,fees,reason,trader,trader_label,clob_id,order_id',
  )
})

test('Place orders sends a basket, an item out per order (D244, D248)', async ({ page }) => {
  const id = await createWorkflow(page, unique('basket'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'markets', type: 'polymarket.markets', params: { limit: 1 } },
    {
      id: 'basket',
      type: 'trading.placeOrders',
      params: {
        // Both outcomes at the lowest price there is: nobody sells there, so both rest.
        orders:
          "{{ outcomes.map(o => ({ tokenId: o.tokenId, side: 'buy', price: tickSize, shares: minOrderSize, type: 'GTC' })) }}",
      },
    },
  ])

  const res = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(res.status()).toBe(200)
  const run = (await res.json()) as {
    status: string
    runs: { nodeId: string; output?: Record<string, number> }[]
  }
  expect(run.status).toBe('succeeded')
  expect(run.runs.find((r) => r.nodeId === 'basket')?.output).toEqual({ resting: 2 })

  const orders = (
    (await (await page.request.get(`/api/v1/workflows/${id}/orders`)).json()) as {
      orders: { status: string; tokenId: string }[]
    }
  ).orders
  expect(orders.map((o) => o.status)).toEqual(['open', 'open'])
  expect(new Set(orders.map((o) => o.tokenId)).size).toBe(2)

  // Cancelled, they free their cash.
  for (const o of (
    (await (await page.request.get(`/api/v1/workflows/${id}/orders`)).json()) as {
      orders: { id: string }[]
    }
  ).orders) {
    expect(
      (await page.request.post(`/api/v1/workflows/${id}/orders/${o.id}/cancel`)).status(),
    ).toBe(200)
  }
})
