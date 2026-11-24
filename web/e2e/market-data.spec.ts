import { expect, test, unique } from './fixtures'

type Market = { id: string; slug: string; outcomes: { tokenId: string; price: string }[] }

// Polymarket's data over the API, as agents have it over MCP (D442): a token with read
// alone reads it, from live Polymarket as the market nodes' tests do.
test('a read token reads markets, books and prices over the API', async ({
  page,
  playwright,
  baseURL,
}) => {
  const made = await page.request.post('/api/v1/tokens', {
    data: { name: unique('notebook'), scopes: ['read'] },
  })
  expect(made.status(), await made.text()).toBe(201)
  const token = ((await made.json()) as { token: string }).token

  const api = await playwright.request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  })

  try {
    const found = await api.get('/api/v1/market-data/search?query=bitcoin&limit=3')
    expect(found.status(), await found.text()).toBe(200)
    const { markets } = (await found.json()) as { markets: (Market & { event: string })[] }
    expect(markets.length).toBeLessThanOrEqual(3)
    test.skip(markets.length === 0, 'Polymarket lists no market for the words now')

    const market = markets[0]
    const one = await api.get(`/api/v1/market-data/markets/${market.slug}`)
    expect(one.status(), await one.text()).toBe(200)
    expect(((await one.json()) as Market).id).toBe(market.id)

    const token1 = market.outcomes[0].tokenId
    const books = await api.get(`/api/v1/market-data/books?tokens=${token1}&depth=3`)
    expect(books.status(), await books.text()).toBe(200)
    const book = (
      (await books.json()) as { books: ({ tokenId: string; bids: unknown[] } | null)[] }
    ).books[0]
    if (book) {
      expect(book.tokenId).toBe(token1)
      expect(book.bids.length).toBeLessThanOrEqual(3)
    }

    const history = await api.get(`/api/v1/market-data/history/${token1}?window=1d&limit=10`)
    expect(history.status(), await history.text()).toBe(200)
    expect(((await history.json()) as { points: unknown[] }).points.length).toBeLessThanOrEqual(10)

    // Asked wrongly, it says so.
    expect((await api.get('/api/v1/market-data/history/x?window=1y')).status()).toBe(400)
    expect((await api.get('/api/v1/market-data/traders/0xnope/positions')).status()).toBe(400)
  } finally {
    await api.dispose()
  }
})
