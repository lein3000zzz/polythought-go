import { createHash, randomBytes } from 'node:crypto'

import type { APIRequestContext, Page } from '@playwright/test'

import { expect, test, unique } from './fixtures'

// The agent's end of the sign-in (D397) is played here: a program with no cookie that
// registers, sends its user's browser to the consent page, and listens on the machine
// itself for the code, as a CLI agent does (RFC 8252). The browser's trip back to it is
// caught before it leaves.
const callback = 'http://127.0.0.1:53682/callback'

type Tokens = {
  access_token: string
  refresh_token: string
  token_type: string
  expires_in: number
  scope: string
}

function pkce() {
  const verifier = randomBytes(32).toString('base64url')
  return { verifier, challenge: createHash('sha256').update(verifier).digest('base64url') }
}

async function token(agent: APIRequestContext, form: Record<string, string>) {
  return agent.post('/api/v1/oauth/token', { form })
}

async function tokensOf(agent: APIRequestContext, form: Record<string, string>): Promise<Tokens> {
  const res = await token(agent, form)
  expect(res.status(), await res.text()).toBe(200)
  expect(res.headers()['cache-control']).toBe('no-store')

  return (await res.json()) as Tokens
}

async function refused(agent: APIRequestContext, form: Record<string, string>, error: string) {
  const res = await token(agent, form)
  expect(res.status(), await res.text()).toBe(400)
  expect(((await res.json()) as { error: string }).error).toBe(error)
}

test('an agent signs in with OAuth, refreshes, and is revoked (D397)', async ({
  page,
  playwright,
  baseURL,
}) => {
  const agent = await playwright.request.newContext({ baseURL })
  const name = unique('E2E agent')
  const resource = `${baseURL}/mcp`

  // It finds the server's endpoints at the root, as RFC 8414 has it.
  const found = await agent.get('/.well-known/oauth-authorization-server')
  expect(found.status()).toBe(200)
  const meta = (await found.json()) as Record<string, unknown>
  expect(meta.issuer).toBe(baseURL)
  expect(meta.authorization_endpoint).toBe(`${baseURL}/oauth/authorize`)
  expect(meta.code_challenge_methods_supported).toEqual(['S256'])

  // It registers, with a loopback redirect: any port will do at sign-in.
  const registered = await agent.post('/api/v1/oauth/register', {
    data: { client_name: name, redirect_uris: ['http://127.0.0.1/callback'] },
  })
  expect(registered.status(), await registered.text()).toBe(201)
  const client = ((await registered.json()) as { client_id: string }).client_id

  await page.route('http://127.0.0.1:53682/**', (route) =>
    route.fulfill({ contentType: 'text/html', body: '<p>Signed in. You can close this tab.</p>' }),
  )

  /** Opens the consent page as the agent would, and gives where the browser came back to. */
  async function authorize(scope: string, state: string, decide?: (page: Page) => Promise<void>) {
    const { verifier, challenge } = pkce()
    const q = new URLSearchParams({
      response_type: 'code',
      client_id: client,
      redirect_uri: callback,
      code_challenge: challenge,
      code_challenge_method: 'S256',
      state,
      scope,
      resource,
    })

    const back = page.waitForRequest((r) => r.url().startsWith(callback))
    await page.goto(`/oauth/authorize?${q}`)
    if (decide) await decide(page)

    const url = new URL((await back).url())
    // On the callback before going on: a page left for another while it still goes there
    // has its next navigation cut short.
    await page.waitForURL((u) => u.href.startsWith(callback))
    expect(url.searchParams.get('state')).toBe(state)
    expect(url.searchParams.get('iss')).toBe(baseURL)

    return { url, verifier }
  }

  const exchange = (code: string, verifier: string) => ({
    grant_type: 'authorization_code',
    client_id: client,
    code,
    redirect_uri: callback,
    code_verifier: verifier,
    resource,
  })
  const refresh = (refreshToken: string) => ({
    grant_type: 'refresh_token',
    client_id: client,
    refresh_token: refreshToken,
  })

  // The user sees who asks and what for, live trading and admin unticked, and adds paper.
  const first = await authorize('read build', 'st-1', async (page) => {
    await expect(page.getByText(`Allow ${name}?`)).toBeVisible()
    await expect(page.getByText('Unverified')).toBeVisible()
    await expect(page.getByText(/Then you go back to http:\/\/127.0.0.1:53682/)).toBeVisible()
    await expect(page.getByRole('checkbox', { name: 'Read' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'Build' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'Trade live' })).not.toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'Admin' })).not.toBeChecked()

    await page.getByRole('checkbox', { name: 'Trade on paper' }).check()
    await page.getByRole('button', { name: 'Allow' }).click()
  })

  const code = first.url.searchParams.get('code') ?? ''
  const signedIn = await tokensOf(agent, exchange(code, first.verifier))
  expect(signedIn.access_token).toMatch(/^poa_/)
  expect(signedIn.refresh_token).toMatch(/^por_/)
  expect(signedIn.token_type).toBe('Bearer')
  expect(signedIn.expires_in).toBe(3600)
  expect(signedIn.scope).toBe('read build paper')

  // A refresh token works once: it gives a new pair.
  const rotated = await tokensOf(agent, refresh(signedIn.refresh_token))
  expect(rotated.refresh_token).not.toBe(signedIn.refresh_token)
  expect(rotated.scope).toBe('read build paper')

  // Settings lists the agent, with what it may do.
  await page.goto('/settings/connected')
  const row = page.getByRole('row').filter({ hasText: name })
  await expect(row).toContainText('Read, Build, Trade on paper')
  await expect(row).toContainText('Unverified')
  await expect(row.getByRole('button', { name: 'Revoke' })).toBeVisible()

  // Signing in again for what it has, it comes back to this machine, where any program
  // could listen: one click, what it has ticked (A15).
  const again = await authorize('read build', 'st-2', async (page) => {
    await expect(page.getByText(`Sign ${name} in again?`)).toBeVisible()
    await expect(page.getByRole('checkbox', { name: 'Read' })).toBeChecked()
    await expect(page.getByRole('checkbox', { name: 'Build' })).toBeChecked()
    await page.getByRole('button', { name: 'Allow' }).click()
  })
  const quiet = await tokensOf(
    agent,
    exchange(again.url.searchParams.get('code') ?? '', again.verifier),
  )
  expect(quiet.scope).toBe('read build')

  // The first refresh token used again means a copy is about: the grant goes, and with
  // it every token it gave.
  await refused(agent, refresh(signedIn.refresh_token), 'invalid_grant')
  await refused(agent, refresh(rotated.refresh_token), 'invalid_grant')
  await refused(agent, refresh(quiet.refresh_token), 'invalid_grant')

  await page.goto('/settings/connected')
  await expect(row).toContainText('Revoked')

  // Revoked, it asks again; allowed again, Settings revokes it.
  const revived = await authorize('read', 'st-3', async (page) => {
    await expect(page.getByText(`Allow ${name}?`)).toBeVisible()
    await page.getByRole('button', { name: 'Allow' }).click()
  })
  const back = await tokensOf(
    agent,
    exchange(revived.url.searchParams.get('code') ?? '', revived.verifier),
  )

  await page.goto('/settings/connected')
  await row.getByRole('button', { name: 'Revoke' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Revoke' }).click()
  await expect(row).toContainText('Revoked')
  await refused(agent, refresh(back.refresh_token), 'invalid_grant')

  // Revoking an unknown token answers as any other (RFC 7009).
  const revoke = await agent.post('/api/v1/oauth/revoke', {
    form: { token: 'por_unknown', client_id: client },
  })
  expect(revoke.status()).toBe(200)

  // Denied, the agent hears so.
  const denied = await authorize('read', 'st-4', async (page) => {
    await page.getByRole('button', { name: 'Deny' }).click()
  })
  expect(denied.url.searchParams.get('error')).toBe('access_denied')
  expect(denied.url.searchParams.get('code')).toBeNull()

  await agent.dispose()
})
