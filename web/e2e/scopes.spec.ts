import type { APIRequestContext, Page } from '@playwright/test'

import { expect, test, unique } from './fixtures'

// An API token does only what its scopes allow on /api/v1 too (D399), through Caddy as a
// script calls it. Made at a browser, as tokens are.

async function makeToken(page: Page, ...scopes: string[]): Promise<string> {
  const res = await page.request.post('/api/v1/tokens', {
    data: { name: unique('scoped'), scopes },
  })
  expect(res.status(), await res.text()).toBe(201)

  return ((await res.json()) as { token: string }).token
}

const chain = [
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
]

function workflow(name: string) {
  return {
    name,
    nodes: chain.map((n, i) => ({ ...n, position: { x: i * 300, y: 0 } })),
    connections: chain
      .slice(1)
      .map((n, i) => ({ from: chain[i].id, fromPort: 'main', to: n.id, toPort: 'main' })),
  }
}

async function as(
  script: APIRequestContext,
  token: string,
  method: string,
  path: string,
  data?: object,
) {
  const res = await script.fetch(path, {
    method,
    headers: { Authorization: `Bearer ${token}` },
    data,
  })
  return { status: res.status(), text: await res.text() }
}

test('a token does only what its scopes allow on the API', async ({
  page,
  playwright,
  baseURL,
}) => {
  const script = await playwright.request.newContext({ baseURL })
  const read = await makeToken(page, 'read')
  const build = await makeToken(page, 'build')
  const paper = await makeToken(page, 'build', 'paper')

  // Read reads, and changes nothing.
  expect((await as(script, read, 'GET', '/api/v1/workflows')).status).toBe(200)
  const refused = await as(script, read, 'POST', '/api/v1/workflows', workflow(unique('nope')))
  expect(refused.status).toBe(403)
  expect(refused.text).toContain('"code":"insufficient_scope"')
  expect(refused.text).toContain('it needs the build scope')

  // A workflow that places orders needs paper besides build; build brings read.
  const noPaper = await as(script, build, 'POST', '/api/v1/workflows', workflow(unique('buys')))
  expect(noPaper.status).toBe(403)
  expect(noPaper.text).toContain('it needs the paper scope')
  expect((await as(script, build, 'GET', '/api/v1/workflows')).status).toBe(200)

  const made = await as(script, paper, 'POST', '/api/v1/workflows', workflow(unique('buys')))
  expect(made.status, made.text).toBe(201)
  const id = (JSON.parse(made.text) as { id: string }).id

  // Going live needs live.
  const live = await as(script, paper, 'PUT', `/api/v1/workflows/${id}/trading/setup`, {
    mode: 'live',
    walletId: 'w-none',
    confirm: true,
  })
  expect(live.status).toBe(403)
  expect(live.text).toContain('it needs the live scope')

  // Settings are their own scope.
  const prefs = await as(script, paper, 'PUT', '/api/v1/notifications/workflow.failed', {
    enabled: false,
  })
  expect(prefs.status).toBe(403)
  expect(prefs.text).toContain('it needs the settings scope')

  await script.dispose()
})

test('any token turns the kill switch on; only live turns it off', async ({
  page,
  playwright,
  baseURL,
}) => {
  const script = await playwright.request.newContext({ baseURL })
  const read = await makeToken(page, 'read')
  const live = await makeToken(page, 'live')

  try {
    expect((await as(script, read, 'PUT', '/api/v1/me/kill-switch', { on: true })).status).toBe(200)

    const off = await as(script, read, 'PUT', '/api/v1/me/kill-switch', { on: false })
    expect(off.status).toBe(403)
    expect(off.text).toContain('it needs the live scope')

    expect((await as(script, live, 'PUT', '/api/v1/me/kill-switch', { on: false })).status).toBe(
      200,
    )
  } finally {
    // Off again whatever happened above, for the specs after this one.
    await page.request.put('/api/v1/me/kill-switch', { data: { on: false } })
    await script.dispose()
  }
})

// A workflow's personal settings, a webhook's URL here, are hidden from tokens unless the
// user lets agents see them, and a token's save keeps them as they were (D402).
test('a token sees a personal setting as hidden, and its save keeps it', async ({
  page,
  playwright,
  baseURL,
}) => {
  const script = await playwright.request.newContext({ baseURL })
  const token = await makeToken(page, 'build')
  const url = 'https://hooks.example/' + unique('secret')

  const made = await page.request.post('/api/v1/workflows', {
    data: {
      name: unique('hooked'),
      nodes: [
        { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
        { id: 'hook', type: 'output.webhook', params: { url }, position: { x: 300, y: 0 } },
      ],
      connections: [{ from: 'manual', fromPort: 'main', to: 'hook', toPort: 'main' }],
    },
  })
  expect(made.status(), await made.text()).toBe(201)
  const id = ((await made.json()) as { id: string }).id

  const read = await as(script, token, 'GET', `/api/v1/workflows/${id}`)
  expect(read.status).toBe(200)
  expect(read.text).not.toContain('secret')
  const w = JSON.parse(read.text) as {
    name: string
    version: number
    nodes: { id: string; params?: Record<string, unknown> }[]
    connections: unknown[]
  }
  expect(w.nodes[1].params?.url).toEqual({ $personal: 'hidden' })

  // Saved back as read, a new name and all: the URL stays.
  const saved = await as(script, token, 'PUT', `/api/v1/workflows/${id}`, {
    name: w.name + ' (renamed)',
    nodes: w.nodes,
    connections: w.connections,
    version: w.version,
  })
  expect(saved.status, saved.text).toBe(200)
  expect(saved.text).not.toContain('secret')

  const own = (await (await page.request.get(`/api/v1/workflows/${id}`)).json()) as typeof w
  expect(own.name).toContain('(renamed)')
  expect(own.nodes[1].params?.url).toBe(url)

  await script.dispose()
})
