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

// trading serves a workflow that trades, with its results, and counts resets.
function trading() {
  const resets: string[] = []

  server.use(
    signedIn,
    http.get('/api/v1/node-types', () => HttpResponse.json({ nodeTypes })),
    http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [] })),
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
