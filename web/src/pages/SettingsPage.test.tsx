import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, apiError, signedIn } from '@/test/fixtures'
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
      noTelegram,
      notifications,
      http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [] })),
      http.put('/api/v1/me/password', async ({ request }) => {
        sent.push(await request.json())
        return sent.length === 1
          ? apiError(401, 'invalid_credentials', 'invalid email or password')
          : new HttpResponse(null, { status: 204 })
      }),
    )
    const { router, user } = renderApp('/settings')

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
    expect(router.state.location.pathname).toBe('/settings') // a wrong password is no sign-out

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
    const { user } = renderApp('/settings')

    await user.type(await screen.findByLabelText('Name'), 'backtest')
    await user.click(screen.getByRole('button', { name: 'Create token' }))

    expect(await screen.findByText('pt_abc_the_whole_secret')).toBeInTheDocument()
    expect(created).toEqual({ name: 'backtest' })
    expect(await screen.findByText('pt_abc…')).toBeInTheDocument()

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
      renderApp('/settings')

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
      renderApp('/settings')

      expect(await screen.findByRole('link', { name: 'Admin → Config' })).toHaveAttribute(
        'href',
        '/admin/config',
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
      const { user } = renderApp('/settings')

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
      const { user } = renderApp('/settings')

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
    const { user } = renderApp('/settings')

    const toggle = await screen.findByRole('switch', { name: 'A workflow fails, on Telegram' })
    expect(toggle).toBeChecked()
    expect(screen.getByText(/link Telegram above to get them/)).toBeInTheDocument()

    // Not by email while mail cannot carry it (D184).
    expect(screen.getByRole('switch', { name: 'A workflow fails, by email' })).toBeDisabled()
    expect(screen.getByText(/Email needs an admin to allow it/)).toBeInTheDocument()

    await user.click(toggle)

    await waitFor(() => expect(toggle).not.toBeChecked())
    expect(sent).toEqual({ enabled: false })
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
    const { user } = renderApp('/settings')

    const email = await screen.findByRole('switch', { name: 'A workflow fails, by email' })
    expect(email).not.toBeChecked()
    expect(screen.getByText(/by email, to alice@example.com/)).toBeInTheDocument()
    expect(screen.queryByText(/Email needs an admin/)).not.toBeInTheDocument()

    await user.click(email)

    await waitFor(() => expect(email).toBeChecked())
    expect(sent).toEqual({ email: true })
    expect(screen.getByRole('switch', { name: 'A workflow fails, on Telegram' })).toBeChecked()
  })
})
