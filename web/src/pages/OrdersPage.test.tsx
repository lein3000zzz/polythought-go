import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { approvalsGranted, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const main: Schemas['Wallet'] = {
  id: 'w-1',
  address: '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed',
  label: 'Main',
  hasCredentials: true,
  hasKey: true,
  hasSessionKey: false,
  heartbeat: false,
  createdAt: '2026-10-01T10:00:00Z',
}

function logged(overrides: Partial<Schemas['LoggedOrder']>): Schemas['LoggedOrder'] {
  return {
    id: 'o-1',
    nodeId: 'copy',
    mode: 'live',
    tokenId: 't-yes',
    marketId: '0xm',
    side: 'buy',
    type: 'FAK',
    postOnly: false,
    price: '0.52',
    shares: '10',
    status: 'filled',
    fills: [{ price: '0.52', shares: '10', dollars: '5.2', fee: '0.03', status: 'confirmed' }],
    at: '2026-10-20T10:00:00Z',
    workflowId: 'wf-1',
    workflow: 'Copy whales',
    deleted: false,
    walletId: 'w-1',
    question: 'Will it rain?',
    outcome: 'Yes',
    ...overrides,
  }
}

function log(orders: Schemas['LoggedOrder'][]) {
  const asked: URLSearchParams[] = []

  server.use(
    signedIn,
    http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [main] })),
    http.get('/api/v1/workflows', () =>
      HttpResponse.json({ workflows: [{ id: 'wf-1', name: 'Copy whales', active: true }] }),
    ),
    http.get('/api/v1/orders', ({ request }) => {
      asked.push(new URL(request.url).searchParams)
      return HttpResponse.json({ orders })
    }),
  )

  return asked
}

describe('live orders page', () => {
  it("lists every live order, deleted workflows' too, narrowed from the address (D228)", async () => {
    const asked = log([
      logged({}),
      logged({
        id: 'o-2',
        workflowId: 'wf-9',
        workflow: 'Old copy',
        deleted: true,
        walletId: 'w-gone',
        status: 'refused',
        reason: 'not enough shares',
        fills: [],
        question: undefined,
        outcome: undefined,
        trader: '0xA1',
        traderLabel: 'Alice',
      }),
    ])
    const { user } = renderApp('/orders?wallet=w-1')

    const filled = (await screen.findByText('Will it rain? · Yes')).closest('tr')!
    expect(within(filled).getByRole('link', { name: 'Copy whales' })).toHaveAttribute(
      'href',
      '/workflows/wf-1',
    )
    expect(within(filled).getByText('Main')).toBeInTheDocument()
    expect(
      within(filled).getByText(/filled: 10 shares for \$5\.20, \$0\.03 in fees/),
    ).toBeInTheDocument()

    const refused = screen.getByText('Old copy (deleted)').closest('tr')!
    expect(within(refused).getByText('An unlinked wallet')).toBeInTheDocument()
    expect(within(refused).getByText('refused: not enough shares')).toBeInTheDocument()
    expect(within(refused).getByText('copying Alice')).toBeInTheDocument()

    expect(asked[0].get('wallet')).toBe('w-1')
    expect(screen.getByLabelText('Wallet')).toHaveValue('w-1')
    expect(screen.getByRole('link', { name: 'Download CSV' })).toHaveAttribute(
      'href',
      '/api/v1/orders/export?wallet=w-1',
    )

    await user.selectOptions(screen.getByLabelText('Status'), 'refused')
    await waitFor(() => expect(asked.at(-1)?.get('status')).toBe('refused'))
    expect(asked.at(-1)?.get('wallet')).toBe('w-1')
    expect(screen.getByRole('link', { name: 'Download CSV' })).toHaveAttribute(
      'href',
      '/api/v1/orders/export?wallet=w-1&status=refused',
    )
  })

  it('says when there are none, and each wallet links to its own', async () => {
    log([])
    server.use(
      approvalsGranted,
      http.get('/api/v1/wallets', () =>
        HttpResponse.json({ wallets: [{ ...main, hasCredentials: false, hasKey: false }] }),
      ),
    )
    const { router, user } = renderApp('/wallets')

    const card = (await screen.findByText('No API credentials.')).closest('[data-slot="card"]')!
    await user.click(within(card as HTMLElement).getByRole('link', { name: 'Live orders' }))

    expect(router.state.location.search).toBe('?wallet=w-1')
    expect(await screen.findByText('No live orders like that.')).toBeInTheDocument()
  })
})
