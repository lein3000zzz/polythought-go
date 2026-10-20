import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { branching, nodeTypes, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const alice = '0x5f3C00000000000000000000000000000000A1b2'
const resetAt = '2026-10-18T10:00:00Z'

const summary: Schemas['TradingSummary'] = {
  account: {
    mode: 'paper',
    cashKnown: false,
    open: true,
    cash: '89.40016',
    startedWith: '100',
    worth: '99.20016',
    profit: '-0.79984',
    startedAt: resetAt,
    resetAt,
  },
  traders: [
    {
      trader: alice,
      label: 'Alice',
      copies: 2,
      volume: '14.8',
      realized: '4.5',
      unrealized: '-0.2',
      wins: 1,
      closed: 1,
    },
    { copies: 1, volume: '5', realized: '0', unrealized: '0', wins: 0, closed: 0 },
  ],
  holdings: [
    {
      mode: 'paper',
      trader: alice,
      traderLabel: 'Alice',
      tokenId: 't-yes',
      marketId: '0xm',
      question: 'Will it?',
      outcome: 'Yes',
      shares: '10',
      cost: '5.1',
      realized: '0',
      mark: '0.49',
      value: '4.9',
      unrealized: '-0.2',
      updatedAt: resetAt,
    },
  ],
}

const order = (id: string, at: string, more: Partial<Schemas['Order']>): Schemas['Order'] => ({
  id,
  nodeId: 'buy',
  mode: 'paper',
  tokenId: 't-yes',
  side: 'buy',
  type: 'FAK',
  postOnly: false,
  price: '0.52',
  status: 'filled',
  fills: [],
  at,
  ...more,
})

const at = '2026-10-18T10:00:00Z'
const main: Schemas['Wallet'] = {
  id: 'w-1',
  address: '0x1111111111111111111111111111111111111111',
  label: 'Main',
  hasCredentials: true,
  hasKey: true,
  hasSessionKey: false,
  createdAt: at,
}
const watchOnly: Schemas['Wallet'] = {
  ...main,
  id: 'w-3',
  label: 'Watch only',
  hasCredentials: false,
  hasKey: false,
}

const paperSetup: Schemas['TradingSetup'] = {
  mode: 'paper',
  holdsLive: false,
  liveAvailable: true,
}

// trading serves a workflow that trades, with its results, and counts resets; it trades
// as setup says, and its owner has wallets.
function trading(setup: Schemas['TradingSetup'] = paperSetup, wallets = [main, watchOnly]) {
  const resets: string[] = []

  server.use(
    signedIn,
    http.get('/api/v1/node-types', () => HttpResponse.json({ nodeTypes })),
    http.get('/api/v1/wallets', () => HttpResponse.json({ wallets })),
    http.get('/api/v1/workflows/wf-1/trading/setup', () => HttpResponse.json(setup)),
    http.get('/api/v1/workflows/wf-1/problems', () => HttpResponse.json({ problems: [] })),
    http.get('/api/v1/workflows/wf-1', () =>
      HttpResponse.json(
        branching({
          nodes: [
            ...branching().nodes,
            { id: 'buy', type: 'trading.placeOrder', position: { x: 900, y: 0 } },
          ],
        }),
      ),
    ),
    http.get('/api/v1/workflows/wf-1/trading', () => HttpResponse.json(summary)),
    http.get('/api/v1/workflows/wf-1/orders', () =>
      HttpResponse.json({
        orders: [
          order('o-3', '2026-10-18T11:00:00Z', {
            trader: alice,
            traderLabel: 'Alice',
            dollars: '10',
            fills: [{ price: '0.5', shares: '19.8', dollars: '9.9', fee: '0.2' }],
          }),
          order('o-2', '2026-10-18T10:30:00Z', {
            side: 'sell',
            type: 'SETTLE',
            price: '1',
            shares: '10',
            fills: [{ price: '1', shares: '10', dollars: '10', fee: '0' }],
          }),
          order('o-1', '2026-10-18T09:00:00Z', {
            side: 'sell',
            shares: '5',
            status: 'refused',
            reason: 'not enough shares: 5 to sell, 0 held',
          }),
        ],
      }),
    ),
    http.post('/api/v1/workflows/wf-1/paper-account/reset', () => {
      resets.push('wf-1')
      return HttpResponse.json({ open: true, cash: '100', startedWith: '100', startedAt: resetAt })
    }),
  )

  return resets
}

describe('trading view', () => {
  it('shows the account, each trader, what is held and the orders (D104)', async () => {
    trading()
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('tab', { name: 'Trading' }))

    const account = within(await screen.findByRole('region', { name: 'Paper account' }))
    expect(account.getByText('$99.20')).toBeInTheDocument()
    expect(account.getByText('$89.40')).toBeInTheDocument()
    expect(account.getByText('−$0.80')).toBeInTheDocument()

    const traders = within(screen.getByRole('region', { name: 'Traders' }))
    const aliceRow = traders.getByRole('row', { name: /Alice/ })
    expect(aliceRow).toHaveTextContent('$14.80')
    expect(aliceRow).toHaveTextContent('+$4.50')
    expect(aliceRow).toHaveTextContent('1 of 1')
    expect(traders.getByRole('row', { name: /Copied from no one/ })).toHaveTextContent('—')

    const holdings = within(screen.getByRole('region', { name: 'Holdings' }))
    expect(holdings.getByRole('row', { name: /Will it\?/ })).toHaveTextContent('0.49')

    const orders = within(screen.getByRole('region', { name: 'Orders' }))
    expect(await orders.findByText(/buy \$10\.00 at up to 0\.52, FAK/)).toBeInTheDocument()
    expect(
      orders.getByText(/filled: 19\.8 shares for \$9\.90, \$0\.20 in fees/),
    ).toBeInTheDocument()
    expect(orders.getByText('settled at 1')).toBeInTheDocument()
    expect(orders.getAllByText('Will it? · Yes').length).toBeGreaterThan(0)
    expect(orders.getByText(/^Before the reset on/)).toBeInTheDocument()
    expect(orders.getByText('refused: not enough shares: 5 to sell, 0 held')).toBeInTheDocument()
  })

  it('resets the paper account, asking first (D107)', async () => {
    const resets = trading()
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('tab', { name: 'Trading' }))
    await user.click(await screen.findByRole('button', { name: 'Reset' }))

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Start the paper account over?')
    await user.click(within(dialog).getByRole('button', { name: 'Reset' }))

    await waitFor(() => expect(resets).toHaveLength(1))
    expect(await screen.findByText('Paper account reset')).toBeInTheDocument()
  })

  it('trades on paper, and live only once Live trading is on (D192)', async () => {
    trading({ ...paperSetup, liveAvailable: false, why: 'live trading is off on this server' })
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('tab', { name: 'Trading' }))

    const how = within(await screen.findByRole('region', { name: 'How it trades' }))
    expect(how.getByText('Paper')).toBeInTheDocument()
    expect(how.getByText(/Trades on paper/)).toBeInTheDocument()
    expect(how.getByText('Live: live trading is off on this server.')).toBeInTheDocument()
    expect(how.getByRole('button', { name: 'Trade live…' })).toBeDisabled()
    expect(screen.queryByRole('group', { name: 'Books' })).not.toBeInTheDocument()
  })

  it('goes live from a wallet that can trade, once confirmed (D201)', async () => {
    trading()
    let asked: unknown
    server.use(
      http.get('/api/v1/wallets/w-1/cash', () => HttpResponse.json({ cash: '152.25' })),
      http.put('/api/v1/workflows/wf-1/trading/setup', async ({ request }) => {
        asked = await request.json()
        return HttpResponse.json({
          mode: 'live',
          wallet: main,
          holdsLive: false,
          liveAvailable: true,
        })
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('tab', { name: 'Trading' }))
    await user.click(await screen.findByRole('button', { name: 'Trade live…' }))

    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('Trade live?')).toBeInTheDocument()
    expect(dialog.getByRole('option', { name: 'Main' })).toBeInTheDocument()
    expect(dialog.queryByRole('option', { name: 'Watch only' })).not.toBeInTheDocument()
    expect(await dialog.findByText('It holds $152.25 of pUSD.')).toBeInTheDocument()

    const go = dialog.getByRole('button', { name: 'Switch to live' })
    expect(go).toBeDisabled()

    await user.click(dialog.getByRole('checkbox', { name: /I understand/ }))
    await user.click(go)

    await waitFor(() => expect(asked).toEqual({ mode: 'live', walletId: 'w-1', confirm: true }))
    expect(await screen.findByText('Trading live from Main')).toBeInTheDocument()
  })

  it('says so when no wallet can trade', async () => {
    trading(paperSetup, [watchOnly])
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('tab', { name: 'Trading' }))
    await user.click(await screen.findByRole('button', { name: 'Trade live…' }))

    const dialog = within(await screen.findByRole('dialog'))
    expect(await dialog.findByText(/None of your wallets can trade yet/)).toBeInTheDocument()
    expect(dialog.getByRole('link', { name: 'Wallets' })).toHaveAttribute('href', '/wallets')
    expect(dialog.getByRole('button', { name: 'Switch to live' })).toBeDisabled()
  })

  it('shows the live books, and goes back to paper at once', async () => {
    trading({ mode: 'live', wallet: main, holdsLive: true, liveAvailable: true })
    let asked: unknown
    const modes: (string | null)[] = []
    server.use(
      http.get('/api/v1/workflows/wf-1/trading', ({ request }) => {
        const mode = new URL(request.url).searchParams.get('mode')
        modes.push(mode)

        return HttpResponse.json(
          mode === 'paper'
            ? summary
            : {
                ...summary,
                account: {
                  mode: 'live',
                  open: true,
                  cash: '250',
                  startedWith: '0',
                  worth: '254.9',
                  profit: '2.4',
                  cashKnown: true,
                  wallet: main,
                },
              },
        )
      }),
      http.get('/api/v1/workflows/wf-1/orders', () =>
        HttpResponse.json({
          orders: [
            order('o-5', '2026-10-18T12:00:00Z', {
              mode: 'live',
              shares: '10',
              status: 'pending',
              reason: 'sent: what it came to is not known yet, and is followed',
            }),
            order('o-4', '2026-10-18T11:30:00Z', {
              mode: 'live',
              shares: '10',
              fills: [{ price: '0.5', shares: '10', dollars: '5', fee: '0.1', status: 'mined' }],
            }),
          ],
        }),
      ),
      http.put('/api/v1/workflows/wf-1/trading/setup', async ({ request }) => {
        asked = await request.json()
        return HttpResponse.json({ ...paperSetup, wallet: main, holdsLive: true })
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('tab', { name: 'Trading' }))

    const account = within(await screen.findByRole('region', { name: 'Live account' }))
    expect(account.getByText('$250.00')).toBeInTheDocument()
    expect(account.getByText(/The pUSD of Main on Polymarket/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reset' })).not.toBeInTheDocument()

    const orders = within(screen.getByRole('region', { name: 'Orders' }))
    expect(
      await orders.findByText('pending: sent: what it came to is not known yet, and is followed'),
    ).toBeInTheDocument()
    expect(
      orders.getByText('filled: 10 shares for $5.00, $0.10 in fees, confirming on chain'),
    ).toBeInTheDocument()
    expect(orders.getAllByText('live')).toHaveLength(2)

    await user.click(screen.getByRole('button', { name: 'Paper books' }))
    expect(await screen.findByRole('region', { name: 'Paper account' })).toBeInTheDocument()
    expect(modes).toContain('paper')

    const how = within(screen.getByRole('region', { name: 'How it trades' }))
    expect(how.getByText(/Trades live from Main/)).toBeInTheDocument()
    await user.click(how.getByRole('button', { name: 'Back to paper' }))

    await waitFor(() => expect(asked).toEqual({ mode: 'paper', walletId: 'w-1', confirm: false }))
    expect(await screen.findByText('Trading on paper')).toBeInTheDocument()
  })

  it('is there only for a saved workflow that trades', async () => {
    server.use(
      signedIn,
      http.get('/api/v1/node-types', () => HttpResponse.json({ nodeTypes })),
      http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [] })),
      http.get('/api/v1/workflows/wf-1/problems', () => HttpResponse.json({ problems: [] })),
      http.get('/api/v1/workflows/wf-1', () => HttpResponse.json(branching())),
    )
    renderApp('/workflows/wf-1')

    expect(await screen.findByRole('tab', { name: 'Code' })).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Trading' })).not.toBeInTheDocument()
  })
})
