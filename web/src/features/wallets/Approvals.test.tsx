import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { browserWallet } from '@/test/ethereum'
import { apiError, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const address = '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed'
const owner = '0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266'

const wallet = (overrides: Partial<Schemas['Wallet']> = {}): Schemas['Wallet'] => ({
  id: 'w-1',
  address,
  label: 'Main',
  hasCredentials: false,
  hasKey: false,
  hasSessionKey: false,
  heartbeat: false,
  createdAt: '2026-10-01T10:00:00Z',
  ...overrides,
})

const missing: Schemas['Approval'][] = [
  {
    token: '0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB',
    spender: '0xE111180000d2663C0091e4f400237545B87B996B',
    standard: 'ERC20',
    name: 'pUSD to the CTF Exchange',
  },
  {
    token: '0x4D97DCd97eC945f40cF65F87097ACe5EA0476045',
    spender: '0xE111180000d2663C0091e4f400237545B87B996B',
    standard: 'ERC1155',
    name: 'Outcome tokens to the CTF Exchange',
  },
]

const setup = (overrides: Partial<Schemas['ApprovalSetup']> = {}): Schemas['ApprovalSetup'] => ({
  owner,
  kind: 3,
  granting: 2,
  state: 'signing',
  createdAt: '2026-10-19T12:00:00Z',
  updatedAt: '2026-10-19T12:00:00Z',
  ...overrides,
})

/** The wallet list and approvals, as status gives them each time they are read. */
function serve(w: Schemas['Wallet'], status: () => Partial<Schemas['Approvals']>) {
  server.use(
    signedIn,
    http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [w] })),
    http.get('/api/v1/wallets/w-1/credentials', () => apiError(404, 'not_found', 'none')),
    http.get('/api/v1/wallets/w-1/approvals', () =>
      HttpResponse.json({
        ready: false,
        missing,
        checkedAt: '2026-10-19T12:00:00Z',
        way: 'browser',
        ...status(),
      }),
    ),
  )
}

describe('trading approvals', () => {
  it('are granted with the imported key', async () => {
    let state: Partial<Schemas['Approvals']> = { way: 'key' }
    let posted = false

    serve(wallet({ hasCredentials: true, hasKey: true }), () => state)
    server.use(
      http.get('/api/v1/wallets/w-1/credentials', () =>
        HttpResponse.json({
          apiKeyHint: 'abcd…wxyz',
          signer: owner,
          signatureType: 2,
          hasKey: true,
          sessionKey: false,
          createdAt: '2026-10-02T10:00:00Z',
          updatedAt: '2026-10-02T10:00:00Z',
        }),
      ),
      http.post('/api/v1/wallets/w-1/approvals/setup', () => {
        posted = true
        state = { way: 'key', setup: setup({ kind: 2, state: 'submitted' }) }
        return HttpResponse.json(state.setup)
      }),
    )
    const { user } = renderApp('/wallets')

    expect(await screen.findByText('2 approvals missing')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Show them' }))
    expect(screen.getByText('pUSD to the CTF Exchange')).toBeInTheDocument()
    expect(screen.getByText('Outcome tokens to the CTF Exchange')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Grant approvals' }))
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Grant' }),
    )

    expect(
      await screen.findByText(/Granting 2 approvals on Polygon, in a minute or so/),
    ).toBeInTheDocument()
    expect(posted).toBe(true)
    expect(screen.getByRole('button', { name: 'Grant approvals' })).toBeDisabled()
  })

  it('are granted with a Deposit Wallet owner’s typed data', async () => {
    const typedData = { primaryType: 'Batch', domain: { chainId: 137 } }
    let started: unknown
    let signed: unknown

    serve(wallet(), () => ({}))
    server.use(
      http.post('/api/v1/wallets/w-1/approvals/signing', async ({ request }) => {
        started = await request.json()
        return HttpResponse.json({ setup: setup(), typedData })
      }),
      http.post('/api/v1/wallets/w-1/approvals/signature', async ({ request }) => {
        signed = await request.json()
        return HttpResponse.json(setup({ state: 'submitted' }))
      }),
    )
    const calls = browserWallet(owner)
    const { user } = renderApp('/wallets')

    await user.click(await screen.findByRole('button', { name: 'Grant approvals' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText(/2 approvals are missing/)).toBeInTheDocument()
    await dialog.findByText('Signs with Fake Wallet.')
    await user.click(dialog.getByRole('button', { name: 'Connect and sign' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(started).toEqual({ owner })
    expect(signed).toEqual({ signature: '0x5ign' })
    expect(calls.at(-1)).toEqual({
      method: 'eth_signTypedData_v4',
      params: [owner, JSON.stringify(typedData)],
    })
    expect(
      await screen.findByText('Signed: 2 approvals are being granted on Polygon'),
    ).toBeInTheDocument()
  })

  it('are granted with a Safe owner’s message', async () => {
    const message = `0x${'ab'.repeat(32)}`

    serve(wallet(), () => ({}))
    server.use(
      http.post('/api/v1/wallets/w-1/approvals/signing', () =>
        HttpResponse.json({ setup: setup({ kind: 2 }), message }),
      ),
      http.post('/api/v1/wallets/w-1/approvals/signature', () =>
        HttpResponse.json(setup({ kind: 2, state: 'submitted' })),
      ),
    )
    const calls = browserWallet(owner, { personal_sign: () => '0xsafe' })
    const { user } = renderApp('/wallets')

    await user.click(await screen.findByRole('button', { name: 'Grant approvals' }))
    const dialog = within(await screen.findByRole('dialog'))
    await dialog.findByText('Signs with Fake Wallet.')
    await user.click(dialog.getByRole('button', { name: 'Connect and sign' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(calls.at(-1)).toEqual({ method: 'personal_sign', params: [message, owner] })
  })

  it('need nothing when the chain has them all', async () => {
    serve(wallet(), () => ({}))
    server.use(
      http.post('/api/v1/wallets/w-1/approvals/signing', () =>
        HttpResponse.json({ setup: setup({ state: 'done', granting: 0 }) }),
      ),
    )
    const calls = browserWallet(owner)
    const { user } = renderApp('/wallets')

    await user.click(await screen.findByRole('button', { name: 'Grant approvals' }))
    const dialog = within(await screen.findByRole('dialog'))
    await dialog.findByText('Signs with Fake Wallet.')
    await user.click(dialog.getByRole('button', { name: 'Connect and sign' }))

    expect(await screen.findByText('Nothing to grant: the wallet has them all')).toBeInTheDocument()
    expect(calls.map((c) => c.method)).not.toContain('eth_signTypedData_v4')
  })

  it('say why they cannot be granted here', async () => {
    serve(wallet(), () => ({
      way: 'none',
      why: 'an EOA wallet grants its approvals itself, with its own wallet',
    }))
    renderApp('/wallets')

    expect(
      await screen.findByText('An EOA wallet grants its approvals itself, with its own wallet.'),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Grant approvals' })).not.toBeInTheDocument()
  })

  it('show when they are granted, and how the last setup went', async () => {
    serve(wallet(), () => ({ ready: true, missing: [], way: 'browser' }))
    renderApp('/wallets')

    expect(await screen.findByText('Approvals granted')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Grant approvals' })).not.toBeInTheDocument()
  })

  it('show a failed setup', async () => {
    serve(wallet(), () => ({
      setup: setup({ state: 'failed', error: 'not confirmed in 15 minutes' }),
    }))
    renderApp('/wallets')

    expect(
      await screen.findByText('Granting the approvals failed: not confirmed in 15 minutes.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Grant approvals' })).toBeEnabled()
  })
})
