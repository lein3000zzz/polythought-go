import '@/test/browser'

import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const canTrade: Schemas['Wallet'] = {
  id: 'w-1',
  address: '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed',
  label: 'Main',
  hasCredentials: true,
  hasKey: true,
  hasSessionKey: false,
  heartbeat: false,
  createdAt: '2026-10-01T10:00:00Z',
}

/** Alice, whose kill switch the server keeps, with a wallet that can trade or not. */
function signedInWith(wallets: Schemas['Wallet'][]) {
  const state = { stopped: false, sent: [] as unknown[] }

  server.use(
    http.get('/api/v1/me', () =>
      HttpResponse.json({
        ...alice,
        liveStopped: state.stopped,
        liveStoppedAt: state.stopped ? '2026-10-20T10:00:00Z' : undefined,
      }),
    ),
    http.get('/api/v1/wallets', () => HttpResponse.json({ wallets })),
    http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    http.put('/api/v1/me/kill-switch', async ({ request }) => {
      const body = (await request.json()) as { on: boolean }
      state.sent.push(body)
      state.stopped = body.on

      return HttpResponse.json(
        body.on
          ? { on: true, since: '2026-10-20T10:00:00Z', cancelled: 2, left: 1 }
          : { on: false, cancelled: 0, left: 0 },
      )
    }),
  )

  return state
}

describe('kill switch', () => {
  it('stops live trading at once, says so on every page, and resumes once asked (D225)', async () => {
    const state = signedInWith([canTrade])
    const { user } = renderApp('/workflows')

    await user.click(await screen.findByRole('button', { name: 'Stop live trading' }))

    expect(state.sent).toEqual([{ on: true }])
    expect(
      await screen.findByText('Live trading stopped: 2 resting orders cancelled'),
    ).toBeInTheDocument()
    expect(
      screen.getByText(/1 still rests on the book: Polymarket did not cancel it/),
    ).toBeInTheDocument()

    const banner = await screen.findByRole('status')
    expect(within(banner).getByText('Live trading is stopped:')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Stop live trading' })).not.toBeInTheDocument()

    await user.click(within(banner).getByRole('button', { name: 'Resume live trading' }))
    const asked = await screen.findByRole('alertdialog', { name: 'Resume live trading?' })
    expect(state.sent, 'asked first').toEqual([{ on: true }])

    await user.click(within(asked).getByRole('button', { name: 'Resume' }))

    expect(await screen.findByText('Live trading resumed')).toBeInTheDocument()
    expect(state.sent).toEqual([{ on: true }, { on: false }])
    expect(await screen.findByRole('button', { name: 'Stop live trading' })).toBeInTheDocument()
    expect(screen.queryByText('Live trading is stopped:')).not.toBeInTheDocument()
  })

  it('is not there without a wallet that can trade', async () => {
    signedInWith([{ ...canTrade, hasKey: false }])
    renderApp('/workflows')

    expect(await screen.findByRole('heading', { name: 'Workflows' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Stop live trading' })).not.toBeInTheDocument()
  })
})
