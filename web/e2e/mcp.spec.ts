import type { APIRequestContext } from '@playwright/test'

import { expect, test, unique } from './fixtures'

// /mcp spoken to as an agent's client does (D398): JSON-RPC over Streamable HTTP, its
// answers as server-sent events, through Caddy. The Go e2e signs an agent in with OAuth;
// these are the browser's parts.

async function rpc(
  agent: APIRequestContext,
  token: string,
  body: object,
  session?: string,
): Promise<{ status: number; text: string; session?: string }> {
  const res = await agent.post('/mcp', {
    headers: {
      Authorization: `Bearer ${token}`,
      Accept: 'application/json, text/event-stream',
      ...(session ? { 'Mcp-Session-Id': session, 'MCP-Protocol-Version': '2025-06-18' } : {}),
    },
    data: body,
  })

  return { status: res.status(), text: await res.text(), session: res.headers()['mcp-session-id'] }
}

test('a token made with scopes on Settings works on /mcp with them', async ({
  page,
  playwright,
  baseURL,
}) => {
  const name = unique('mcp token')

  await page.goto('/settings/tokens')
  await page.getByLabel('Name').fill(name)
  await page.getByRole('checkbox', { name: 'Build' }).uncheck()
  await page.getByRole('checkbox', { name: 'Trade on paper' }).uncheck()
  await expect(page.getByRole('checkbox', { name: 'Admin' })).not.toBeChecked() // an admin's
  await page.getByRole('button', { name: 'Create token' }).click()

  const secret = (await page.locator('code').filter({ hasText: /^pt_/ }).textContent()) ?? ''
  await page.getByRole('button', { name: 'Done' }).click()
  await expect(page.getByRole('row').filter({ hasText: name })).toContainText('Read')

  const agent = await playwright.request.newContext({ baseURL })

  const init = await rpc(agent, secret, {
    jsonrpc: '2.0',
    id: 1,
    method: 'initialize',
    params: {
      protocolVersion: '2025-06-18',
      capabilities: {},
      clientInfo: { name: 'e2e', version: '1' },
    },
  })
  expect(init.status, init.text).toBe(200)
  expect(init.text).toContain('"name":"polythought"')
  expect(init.session).toBeTruthy()

  const ready = await rpc(
    agent,
    secret,
    { jsonrpc: '2.0', method: 'notifications/initialized' },
    init.session,
  )
  expect(ready.status).toBe(202)

  const me = await rpc(
    agent,
    secret,
    { jsonrpc: '2.0', id: 2, method: 'tools/call', params: { name: 'whoami', arguments: {} } },
    init.session,
  )
  expect(me.status, me.text).toBe(200)
  expect(me.text).toContain('"via":"token"')
  expect(me.text).toContain('"scopes":["read"]')

  await agent.dispose()
})

test('an admin switches agents over MCP off, and /mcp and its sign-in go', async ({
  page,
  playwright,
  baseURL,
}) => {
  const agent = await playwright.request.newContext({ baseURL })

  try {
    await page.goto('/admin/config/agents-over-mcp')
    await expect(page.getByLabel('Access token lifetime')).toBeVisible()
    await page.getByRole('switch', { name: 'Agents over MCP' }).click()
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Settings saved')).toBeVisible()

    expect((await agent.get('/.well-known/oauth-authorization-server')).status()).toBe(404)
    expect((await agent.get('/.well-known/oauth-protected-resource/mcp')).status()).toBe(404)
    expect((await agent.post('/mcp', { data: {} })).status()).toBe(404)

    await page.getByRole('switch', { name: 'Agents over MCP' }).click()
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Settings saved')).toBeVisible()

    expect((await agent.get('/.well-known/oauth-authorization-server')).status()).toBe(200)
    expect((await agent.post('/mcp', { data: {} })).status()).toBe(401)
  } finally {
    // On again whatever happened above, for the specs after this one.
    await page.request.patch('/api/v1/admin/settings', {
      data: { values: { 'mcp.enabled': null } },
    })
    await agent.dispose()
  }
})
