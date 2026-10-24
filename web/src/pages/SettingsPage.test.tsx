import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, apiError, registration, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const token: Schemas['APIToken'] = {
  id: 't-1',
  name: 'backtest',
  prefix: 'pt_abc',
  createdAt: '2026-10-01T10:00:00Z',
}

const noTelegram = http.get('/api/v1/telegram', () => HttpResponse.json({ available: false }))

const failedPref: Schemas['Notification'] = {
  key: 'workflow_failed',
  group: 'Workflows',
  label: 'A workflow fails',
  description: 'A run of one of your workflows failed.',
  enabled: true,
  email: false,
}

const notifications = http.get('/api/v1/notifications', () =>
  HttpResponse.json({ notifications: [failedPref], mailOn: false }),
)

describe('settings page', () => {
  it('changes the password', async () => {
    const sent: unknown[] = []

    server.use(
      signedIn,
      registration('invite'), // the shortest password
      http.put('/api/v1/me/password', async ({ request }) => {
        sent.push(await request.json())
        return sent.length === 1
          ? apiError(401, 'invalid_credentials', 'invalid email or password')
          : new HttpResponse(null, { status: 204 })
      }),
    )
    const { router, user } = renderApp('/settings/account')

    await user.type(await screen.findByLabelText('Current password'), 'old password!')
    await user.type(screen.getByLabelText('New password'), 'a brand new password')
    await user.type(screen.getByLabelText('Repeat the new password'), 'a brand new pasword')
    await user.click(screen.getByRole('button', { name: 'Change password' }))

    expect(screen.getByRole('alert')).toHaveTextContent('The new passwords differ')
    expect(sent).toHaveLength(0)

    await user.clear(screen.getByLabelText('Repeat the new password'))
    await user.type(screen.getByLabelText('Repeat the new password'), 'a brand new password')
    await user.click(screen.getByRole('button', { name: 'Change password' }))
    expect(await screen.findByText('That is not your current password.')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/settings/account') // a wrong password is no sign-out

    await user.click(screen.getByRole('button', { name: 'Change password' }))
    expect(await screen.findByText('Password changed')).toBeInTheDocument()
    expect(sent[1]).toEqual({
      currentPassword: 'old password!',
      newPassword: 'a brand new password',
    })
    expect(screen.getByLabelText('Current password')).toHaveValue('')
  })

  it('shows a new token once and revokes tokens', async () => {
    let tokens: Schemas['APIToken'][] = []
    let created: Schemas['CreateTokenRequest'] | undefined

    server.use(
      signedIn,
      noTelegram,
      notifications,
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens })),
      http.post('/api/v1/tokens', async ({ request }) => {
        created = (await request.json()) as Schemas['CreateTokenRequest']
        tokens = [token]
        return HttpResponse.json({ ...token, token: 'pt_abc_the_whole_secret' }, { status: 201 })
      }),
      http.delete('/api/v1/tokens/t-1', () => {
        tokens = []
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/settings/tokens')

    await user.type(await screen.findByLabelText('Name'), 'backtest')

    // Read, build and paper to start with; live by hand; admin only for admins (D398).
    expect(screen.getByRole('checkbox', { name: 'Read' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Trade live' })).not.toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Change settings' })).not.toBeChecked() // D399
    expect(screen.queryByRole('checkbox', { name: 'Admin' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: 'Build' }))
    await user.click(screen.getByRole('checkbox', { name: 'Trade live' }))
    await user.click(screen.getByRole('button', { name: 'Create token' }))

    expect(await screen.findByText('pt_abc_the_whole_secret')).toBeInTheDocument()
    expect(created).toEqual({ name: 'backtest', scopes: ['read', 'paper', 'live'] })
    expect(await screen.findByText('pt_abc…')).toBeInTheDocument()
    expect(screen.getByText('pt_abc…').closest('tr')).toHaveTextContent('All') // made before scopes
    expect(screen.getByRole('checkbox', { name: 'Build' })).toBeChecked() // as it starts again

    for (const name of ['Read', 'Build', 'Trade on paper']) {
      await user.click(screen.getByRole('checkbox', { name }))
    }
    expect(screen.getByRole('button', { name: 'Create token' })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: 'Done' }))
    expect(screen.queryByText('pt_abc_the_whole_secret')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Revoke' }))
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Revoke' }),
    )
    expect(await screen.findByText('No tokens yet.')).toBeInTheDocument()
  })

  describe('telegram', () => {
    const base = [
      signedIn,
      notifications,
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [] })),
    ]

    it('says when the instance has no bot', async () => {
      server.use(...base, noTelegram)
      renderApp('/settings/telegram')

      expect(await screen.findByText(/Telegram is not set up on this instance/)).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Link Telegram' })).not.toBeInTheDocument()
      expect(screen.queryByText(/Admin → Config/)).not.toBeInTheDocument()
    })

    it('tells admins what went wrong', async () => {
      server.use(
        http.get('/api/v1/me', () => HttpResponse.json({ ...alice, role: 'admin' })),
        notifications,
        http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [] })),
        http.get('/api/v1/telegram', () =>
          HttpResponse.json({
            available: false,
            problem: 'Telegram refused the bot token; check it with @BotFather',
            problemAt: '2026-10-14T10:00:00Z',
          }),
        ),
      )
      renderApp('/settings/telegram')

      expect(await screen.findByRole('link', { name: 'Admin → Config' })).toHaveAttribute(
        'href',
        '/admin/config/telegram#telegram.enabled',
      )
      expect(screen.getByText(/Telegram refused the bot token/)).toBeInTheDocument()
    })

    it('links through a one-time link, and notices when it lands', async () => {
      let linked = false
      server.use(
        ...base,
        http.get('/api/v1/telegram', () =>
          HttpResponse.json({
            available: true,
            botUsername: 'poly_bot',
            ...(linked && {
              link: { username: 'alice_tg', firstName: 'Alice', linkedAt: '2026-10-14T10:00:00Z' },
            }),
          }),
        ),
        http.post('/api/v1/telegram/link', () =>
          HttpResponse.json(
            { url: 'https://t.me/poly_bot?start=abc', expiresAt: '2099-01-01T00:00:00Z' },
            { status: 201 },
          ),
        ),
      )
      const { user } = renderApp('/settings/telegram')

      await user.click(await screen.findByRole('button', { name: 'Link Telegram' }))

      const open = await screen.findByRole('link', { name: 'Open Telegram' })
      expect(open).toHaveAttribute('href', 'https://t.me/poly_bot?start=abc')
      expect(open).toHaveAttribute('target', '_blank')

      linked = true // the user pressed Start in Telegram
      expect(await screen.findByText('@alice_tg', {}, { timeout: 4000 })).toBeInTheDocument()
      expect(screen.queryByRole('link', { name: 'Open Telegram' })).not.toBeInTheDocument()
    })

    it('unlinks after asking', async () => {
      let unlinked = false
      server.use(
        ...base,
        http.get('/api/v1/telegram', () =>
          HttpResponse.json({
            available: true,
            botUsername: 'poly_bot',
            ...(!unlinked && { link: { firstName: 'Alice', linkedAt: '2026-10-14T10:00:00Z' } }),
          }),
        ),
        http.delete('/api/v1/telegram/link', () => {
          unlinked = true
          return new HttpResponse(null, { status: 204 })
        }),
      )
      const { user } = renderApp('/settings/telegram')

      expect(await screen.findByText('Alice')).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Unlink' }))
      await user.click(
        within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Unlink' }),
      )

      expect(await screen.findByText('Telegram unlinked')).toBeInTheDocument()
      expect(await screen.findByRole('button', { name: 'Link Telegram' })).toBeInTheDocument()
    })
  })

  it('switches a notification off, and says where they go', async () => {
    let sent: unknown
    server.use(
      signedIn,
      noTelegram,
      notifications,
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [] })),
      http.put('/api/v1/notifications/workflow_failed', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({ ...failedPref, enabled: false })
      }),
    )
    const { user } = renderApp('/settings/notifications')

    const toggle = await screen.findByRole('switch', { name: 'A workflow fails, on Telegram' })
    expect(toggle).toBeChecked()
    // Telegram is a page of its own, linked from here while it is not linked (D392).
    expect(screen.getByRole('link', { name: 'link Telegram' })).toHaveAttribute(
      'href',
      '/settings/telegram',
    )

    // Not by email while mail cannot carry it (D184).
    expect(screen.getByRole('switch', { name: 'A workflow fails, by email' })).toBeDisabled()
    expect(screen.getByText(/Email needs an admin to allow it/)).toBeInTheDocument()

    await user.click(toggle)

    await waitFor(() => expect(toggle).not.toBeChecked())
    expect(sent).toEqual({ enabled: false })
  })

  it('shows the kinds under their groups, live and paper orders apart (D229)', async () => {
    const pref = (key: string, group: string, label: string, enabled: boolean) => ({
      ...failedPref,
      key,
      group,
      label,
      enabled,
    })

    server.use(
      signedIn,
      noTelegram,
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [] })),
      http.get('/api/v1/notifications', () =>
        HttpResponse.json({
          notifications: [
            failedPref,
            pref('live_order_filled', 'Live orders', 'A live order fills', true),
            pref('live_order_rests', 'Live orders', 'A live order rests on the book', false),
            pref('paper_order_filled', 'Paper orders', 'A paper order fills', false),
          ],
          mailOn: false,
        }),
      ),
    )
    renderApp('/settings/notifications')

    expect(await screen.findByRole('heading', { name: 'Live orders' })).toBeInTheDocument()
    expect(screen.getAllByRole('heading', { name: 'Live orders' })).toHaveLength(1)
    expect(screen.getByRole('heading', { name: 'Paper orders' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Workflows' })).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'A live order fills, on Telegram' })).toBeChecked()
    expect(
      screen.getByRole('switch', { name: 'A paper order fills, on Telegram' }),
    ).not.toBeChecked()
  })

  it('switches a notification on by email, to the account address (D170)', async () => {
    let sent: unknown
    server.use(
      signedIn,
      noTelegram,
      http.get('/api/v1/notifications', () =>
        HttpResponse.json({ notifications: [failedPref], mailOn: true }),
      ),
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [] })),
      http.put('/api/v1/notifications/workflow_failed', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({ ...failedPref, email: true })
      }),
    )
    const { user } = renderApp('/settings/notifications')

    const email = await screen.findByRole('switch', { name: 'A workflow fails, by email' })
    expect(email).not.toBeChecked()
    expect(screen.getByText(/by email, to alice@example.com/)).toBeInTheDocument()
    expect(screen.queryByText(/Email needs an admin/)).not.toBeInTheDocument()

    await user.click(email)

    await waitFor(() => expect(email).toBeChecked())
    expect(sent).toEqual({ email: true })
    expect(screen.getByRole('switch', { name: 'A workflow fails, on Telegram' })).toBeChecked()
  })

  it("shows what your agents spent this month against their caps, and your kill switch's reach (D276, D277)", async () => {
    const spent: Schemas['AgentSpend'] = {
      month: '2026-10-01T00:00:00Z',
      resets: '2026-11-01T00:00:00Z',
      inputTokens: 1_200_000,
      outputTokens: 300_000,
      totalTokens: 1_500_000,
      dollars: '4.20',
      calls: 70,
      costedCalls: 20,
      caps: { dollars: '20.00', inputTokens: 0, outputTokens: 0, totalTokens: 20_000_000 },
      own: {},
      killStopsAgents: false,
    }
    let sent: unknown
    server.use(
      signedIn,
      noTelegram,
      notifications,
      registration('invite'),
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [] })),
      http.get('/api/v1/me/agent-spend', () => HttpResponse.json(spent)),
      http.put('/api/v1/me/agents', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({ ...spent, killStopsAgents: true })
      }),
    )
    const { user } = renderApp('/settings/agents')

    const spend = await screen.findByLabelText('Agent spend')
    expect(spend).toHaveTextContent('$4.20 of $20.00 (20 of 70 calls priced by their provider)')
    expect(spend).toHaveTextContent('Tokens1,500,000 of 20,000,000')
    expect(spend).toHaveTextContent('Input tokens1,200,000 no cap')

    const choice = screen.getByRole('switch', { name: 'My kill switch stops my agents too' })
    expect(choice).not.toBeChecked()
    await user.click(choice)

    await waitFor(() => expect(choice).toBeChecked())
    expect(sent).toEqual({ killStopsAgents: true })
    expect(await screen.findByText('Your kill switch stops your agents too')).toBeInTheDocument()
  })

  it('has no agents section where there are no agents', async () => {
    server.use(
      signedIn,
      noTelegram,
      notifications,
      registration('invite'),
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [] })),
      http.get('/api/v1/me/agent-spend', () => apiError(404, 'not_found', 'not found')),
    )
    const { router } = renderApp('/settings/agents')

    // The page goes once the server says there are no agents, and so does its card.
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings'))
    const cards = within(await screen.findByRole('list', { name: 'Overview' }))
    expect(await cards.findByRole('link', { name: /^Notifications/ })).toBeInTheDocument()
    expect(cards.queryByRole('link', { name: /AI agents/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'AI agents' })).not.toBeInTheDocument() // the menu
  })

  it('shows each page on the overview, with how things are there (D392)', async () => {
    server.use(
      signedIn,
      noTelegram,
      notifications,
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [token] })),
    )
    renderApp('/settings')

    const cards = within(await screen.findByRole('list', { name: 'Overview' }))
    const card = (name: RegExp) => cards.findByRole('link', { name })
    expect(await card(/^Account/)).toHaveTextContent(`Signed in as ${alice.email}`)
    expect(await card(/^Account/)).toHaveAttribute('href', '/settings/account')
    expect(await card(/^Telegram/)).toHaveTextContent('Not set up on this instance')
    expect(await card(/^Notifications/)).toHaveTextContent('1 on Telegram, 0 by email, of 1')
    expect(await card(/^API tokens/)).toHaveTextContent('1 token')
    expect(await card(/^Credentials/)).toHaveTextContent('None yet')
  })

  it('finds a notification or a token from any page, and opens its page at it (D392)', async () => {
    server.use(
      signedIn,
      noTelegram,
      notifications,
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [token] })),
    )
    const { user, router } = renderApp('/settings/account')

    await screen.findByRole('heading', { name: 'Password' })
    await user.keyboard('/')
    const search = screen.getByRole('searchbox', { name: 'Search settings' })
    await user.type(search, 'workflow fails')

    const found = within(screen.getByRole('navigation', { name: 'Matching settings' }))
    expect(found.getByText('Notifications')).toBeInTheDocument()
    await user.keyboard('{Enter}')
    expect(router.state.location.pathname).toBe('/settings/notifications')
    await waitFor(() =>
      expect(screen.getByRole('switch', { name: 'A workflow fails, on Telegram' })).toHaveFocus(),
    )

    await user.clear(search)
    await user.type(search, 'backtest')
    await user.click(
      within(screen.getByRole('navigation', { name: 'Matching settings' })).getByRole('link', {
        name: 'backtest',
      }),
    )
    expect(router.state.location.pathname).toBe('/settings/tokens')
    expect(router.state.location.hash).toBe('#token-t-1')
  })

  it('lists the agents allowed, and revokes one (D384)', async () => {
    const grant: Schemas['OAuthGrant'] = {
      id: 'g-1',
      client: { id: 'pc_1', name: 'Claude Code', verified: false, site: 'claude.ai' },
      scopes: ['read', 'paper'],
      createdAt: '2026-10-08T12:00:00Z',
      lastUsedAt: '2026-10-08T13:00:00Z',
    }
    const old: Schemas['OAuthGrant'] = {
      id: 'g-0',
      client: { id: 'https://agent.example/client.json', name: 'Old Agent', verified: true },
      scopes: ['read'],
      createdAt: '2026-10-01T12:00:00Z',
      revokedAt: '2026-10-02T12:00:00Z',
    }
    let grants = [grant, old]
    let revoked = ''

    server.use(
      signedIn,
      noTelegram,
      notifications,
      http.get('/api/v1/me/grants', () => HttpResponse.json({ grants })),
      http.delete('/api/v1/me/grants/:id', ({ params }) => {
        revoked = String(params.id)
        grants = [{ ...grant, revokedAt: '2026-10-09T12:00:00Z' }, old]
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/settings')

    const cards = within(await screen.findByRole('list', { name: 'Overview' }))
    expect(await cards.findByRole('link', { name: /^Connected agents/ })).toHaveTextContent(
      '1 agent',
    )

    await user.click(await cards.findByRole('link', { name: /^Connected agents/ }))
    const row = (await screen.findByText('Claude Code')).closest('tr') as HTMLElement
    expect(row).toHaveTextContent('Unverified')
    expect(row).toHaveTextContent('claude.ai')
    expect(row).toHaveTextContent('Read, Trade on paper')
    expect(screen.getByText('Old Agent').closest('tr')).toHaveTextContent(/Revoked/)

    await user.click(within(row).getByRole('button', { name: 'Revoke' }))
    const dialog = within(await screen.findByRole('alertdialog'))
    expect(dialog.getByText('Revoke “Claude Code”?')).toBeInTheDocument()
    await user.click(dialog.getByRole('button', { name: 'Revoke' }))

    await waitFor(() => expect(revoked).toBe('g-1'))
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Revoke' })).not.toBeInTheDocument(),
    )
  })

  it('lets agents see personal settings, off to start with (D401)', async () => {
    let sent: unknown
    server.use(
      signedIn,
      http.put('/api/v1/me/agent-access', async ({ request }) => {
        sent = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/settings/connected')

    const on = await screen.findByRole('switch', {
      name: 'Agents and tokens see personal settings',
    })
    expect(on).not.toBeChecked()
    await user.click(on)

    await waitFor(() => expect(sent).toEqual({ seePersonal: true }))
  })

  it('lets agents delete, share, use credentials and answer, each off to start with (D403 to D405)', async () => {
    const sent: unknown[] = []
    server.use(
      signedIn,
      http.put('/api/v1/me/agent-access', async ({ request }) => {
        sent.push(await request.json())
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/settings/connected')

    const remove = await screen.findByRole('switch', { name: 'Agents and tokens may delete' })
    const share = screen.getByRole('switch', { name: 'Agents and tokens may share to the library' })
    const use = screen.getByRole('switch', {
      name: 'Agents and tokens may use my credentials in workflows',
    })
    const answer = screen.getByRole('switch', {
      name: "Agents and tokens may answer my workflows' questions",
    })
    for (const s of [remove, share, use, answer]) expect(s).not.toBeChecked()

    await user.click(remove)
    await waitFor(() => expect(sent).toEqual([{ delete: true }]))
    await user.click(share)
    await waitFor(() => expect(sent).toEqual([{ delete: true }, { share: true }]))
    await user.click(use)
    await waitFor(() =>
      expect(sent).toEqual([{ delete: true }, { share: true }, { useCredentials: true }]),
    )
    await user.click(answer)
    await waitFor(() => expect(sent).toHaveLength(4))
    expect(sent[3]).toEqual({ answer: true })
  })

  it('has Credentials as a page, where its old address goes (D392)', async () => {
    server.use(signedIn)
    const { router } = renderApp('/credentials')

    expect(await screen.findByRole('heading', { name: 'Credentials' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/settings/credentials')
    const menu = within(screen.getByRole('navigation', { name: 'Settings pages' }))
    expect(menu.getByRole('link', { name: 'Credentials' })).toHaveAttribute('aria-current', 'page')
  })
})
