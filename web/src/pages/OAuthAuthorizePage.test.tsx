import { screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { Schemas } from '@/api/client'
import { leaveFor } from '@/lib/leave'
import { signedIn, signedOut } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

vi.mock('@/lib/leave', () => ({ leaveFor: vi.fn() }))

const query =
  'response_type=code&client_id=pc_1&redirect_uri=http%3A%2F%2F127.0.0.1%3A4000%2Fcb&code_challenge=ch&code_challenge_method=S256&state=st&scope=read+live'
const path = `/oauth/authorize?${query}`

const request = {
  response_type: 'code',
  client_id: 'pc_1',
  redirect_uri: 'http://127.0.0.1:4000/cb',
  code_challenge: 'ch',
  code_challenge_method: 'S256',
  scope: 'read live',
  state: 'st',
}

function consent(overrides: Partial<Schemas['OAuthConsent']> = {}): Schemas['OAuthConsent'] {
  return {
    client: { id: 'pc_1', name: 'Claude Code', published: false },
    redirectHost: 'http://127.0.0.1:4000',
    offered: [
      { scope: 'read', ticked: true },
      { scope: 'build', ticked: false },
      { scope: 'paper', ticked: false },
      { scope: 'live', ticked: false },
    ],
    allowed: false,
    ...overrides,
  }
}

/** Serves the consent, and keeps what the page sent. */
function serve(c: Schemas['OAuthConsent']) {
  const sent: { consent?: unknown; approve?: unknown; deny?: unknown } = {}

  server.use(
    signedIn,
    http.post('/api/v1/oauth/authorization', async ({ request }) => {
      sent.consent = await request.json()
      return HttpResponse.json(c)
    }),
    http.post('/api/v1/oauth/authorization/approve', async ({ request }) => {
      sent.approve = await request.json()
      return HttpResponse.json({ redirect: 'http://127.0.0.1:4000/cb?code=c0de&state=st' })
    }),
    http.post('/api/v1/oauth/authorization/deny', async ({ request }) => {
      sent.deny = await request.json()
      return HttpResponse.json({
        redirect: 'http://127.0.0.1:4000/cb?error=access_denied&state=st',
      })
    }),
  )

  return sent
}

describe('the consent page (D384)', () => {
  beforeEach(() => vi.mocked(leaveFor).mockClear())

  it('asks for the request it was sent with, and allows the scopes ticked', async () => {
    const sent = serve(consent())
    const { user } = renderApp(path)

    expect(await screen.findByText('Allow Claude Code?')).toBeInTheDocument()
    expect(sent.consent).toEqual(request)
    expect(screen.getByText('Unverified')).toBeInTheDocument()
    expect(screen.getByText(/Allow it only if you just started it/)).toBeInTheDocument()
    expect(screen.getByText(/Then you go back to http:\/\/127.0.0.1:4000/)).toBeInTheDocument()

    expect(screen.getByRole('checkbox', { name: 'Read' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Trade live' })).not.toBeChecked()
    expect(screen.queryByRole('checkbox', { name: 'Admin' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('checkbox', { name: 'Trade on paper' }))
    await user.click(screen.getByRole('button', { name: 'Allow' }))

    await waitFor(() =>
      expect(leaveFor).toHaveBeenCalledWith('http://127.0.0.1:4000/cb?code=c0de&state=st'),
    )
    expect(sent.approve).toEqual({ request, scopes: ['read', 'paper'] })
    expect(await screen.findByText('Back to Claude Code')).toBeInTheDocument()
  })

  it('says where a published agent describes itself, never that it is verified (A2)', async () => {
    serve(
      consent({
        client: {
          id: 'https://claude.ai/oauth/claude-code-client-metadata',
          name: 'Claude Code',
          published: true,
          site: 'claude.ai',
        },
      }),
    )
    renderApp(path)

    expect(await screen.findByText('Published at claude.ai')).toBeInTheDocument()
    expect(
      screen.getByText(/whoever controls claude.ai wrote that, name and all/),
    ).toBeInTheDocument()
    expect(screen.queryByText('Verified')).not.toBeInTheDocument()
    expect(screen.queryByText(/sends you back to/)).not.toBeInTheDocument()
  })

  it('warns when a published agent sends you elsewhere than its own host (A2)', async () => {
    serve(
      consent({
        client: {
          id: 'https://raw.githubusercontent.com/someone/x/main/cc.json',
          name: 'Claude Code',
          published: true,
          site: 'raw.githubusercontent.com',
        },
        redirectHost: 'https://evil.example',
        redirectAway: true,
      }),
    )
    renderApp(path)

    expect(
      await screen.findByText(
        /It sends you back to https:\/\/evil.example, not to raw.githubusercontent.com/,
      ),
    ).toBeInTheDocument()
  })

  it('allows nothing with nothing ticked', async () => {
    serve(consent())
    const { user } = renderApp(path)

    await user.click(await screen.findByRole('checkbox', { name: 'Read' }))
    expect(screen.getByRole('button', { name: 'Allow' })).toBeDisabled()
  })

  it('tells the agent the user said no', async () => {
    const sent = serve(consent())
    const { user } = renderApp(path)

    await user.click(await screen.findByRole('button', { name: 'Deny' }))

    await waitFor(() =>
      expect(leaveFor).toHaveBeenCalledWith(
        'http://127.0.0.1:4000/cb?error=access_denied&state=st',
      ),
    )
    expect(sent.deny).toEqual(request)
  })

  it('lets an agent allowed before through at once, once', async () => {
    const sent = serve(consent({ allowed: true, granted: ['read'] }))
    renderApp(path)

    await waitFor(() =>
      expect(leaveFor).toHaveBeenCalledWith('http://127.0.0.1:4000/cb?code=c0de&state=st'),
    )
    expect(sent.approve).toEqual({ request, scopes: ['read'] })
    expect(leaveFor).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('button', { name: 'Allow' })).not.toBeInTheDocument()
  })

  it('shows a request it cannot send back', async () => {
    serve({
      allowed: false,
      problem: {
        code: 'invalid_request',
        description: 'the redirect_uri is not one the client registered',
      },
    })
    renderApp(path)

    expect(await screen.findByText('This sign-in link does not work')).toBeInTheDocument()
    expect(
      screen.getByText('the redirect_uri is not one the client registered'),
    ).toBeInTheDocument()
    expect(leaveFor).not.toHaveBeenCalled()
  })

  it('sends a wrong request back to the agent', async () => {
    serve({
      allowed: false,
      problem: {
        code: 'invalid_scope',
        description: 'no scope x',
        redirect: 'http://127.0.0.1:4000/cb?error=invalid_scope&state=st',
      },
    })
    renderApp(path)

    await waitFor(() =>
      expect(leaveFor).toHaveBeenCalledWith(
        'http://127.0.0.1:4000/cb?error=invalid_scope&state=st',
      ),
    )
  })

  it('asks before sending an error back to an agent never allowed (A14)', async () => {
    serve({
      allowed: false,
      problem: {
        code: 'unsupported_response_type',
        description: 'the response_type is code',
        redirect: 'https://evil.example/login?error=unsupported_response_type',
        askFirst: true,
      },
    })
    renderApp(path)

    expect(await screen.findByText('This sign-in link does not work')).toBeInTheDocument()
    expect(screen.getByText('https://evil.example')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'go back to the agent' })).toHaveAttribute(
      'href',
      'https://evil.example/login?error=unsupported_response_type',
    )
    expect(leaveFor).not.toHaveBeenCalled()
  })

  it('asks for a click to sign an agent in again where it comes back off-site (A15)', async () => {
    const sent = serve(
      consent({
        again: true,
        granted: ['read', 'build'],
        offered: [
          { scope: 'read', ticked: true },
          { scope: 'build', ticked: true },
          { scope: 'paper', ticked: false },
          { scope: 'live', ticked: false },
        ],
      }),
    )
    const { user } = renderApp(path)

    expect(await screen.findByText('Sign Claude Code in again?')).toBeInTheDocument()
    expect(screen.getByText(/comes back to http:\/\/127\.0\.0\.1:4000/)).toBeInTheDocument()
    expect(leaveFor).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Allow' }))

    await waitFor(() =>
      expect(leaveFor).toHaveBeenCalledWith('http://127.0.0.1:4000/cb?code=c0de&state=st'),
    )
    expect(sent.approve).toEqual({ request, scopes: ['read', 'build'] })
  })

  it('shows what the server refused', async () => {
    serve(consent())
    server.use(
      http.post('/api/v1/oauth/authorization/approve', () =>
        HttpResponse.json({
          problem: { code: 'invalid_scope', description: 'allow at least one scope, or deny' },
        }),
      ),
    )
    const { user } = renderApp(path)

    await user.click(await screen.findByRole('button', { name: 'Allow' }))

    expect(await screen.findByText('allow at least one scope, or deny')).toBeInTheDocument()
    expect(leaveFor).not.toHaveBeenCalled()
  })

  it('signs in first, and comes back to the request', async () => {
    server.use(signedOut)
    const { router } = renderApp(path)

    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(new URLSearchParams(router.state.location.search).get('next')).toBe(path)
  })
})
