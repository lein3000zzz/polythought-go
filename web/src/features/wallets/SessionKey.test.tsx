import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { browserWallet, declined } from '@/test/ethereum'
import { apiError, approvalsGranted, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const address = '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed'
const owner = '0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266'
const typedData = { primaryType: 'Batch', domain: { chainId: 137 } }

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

const sessionKey = (overrides: Partial<Schemas['SessionKey']> = {}): Schemas['SessionKey'] => ({
  signer: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8',
  owner,
  state: 'signing',
  validUntil: '2099-04-17T07:00:00Z',
  createdAt: '2026-10-19T12:00:00Z',
  updatedAt: '2026-10-19T12:00:00Z',
  ...overrides,
})

describe('session keys', () => {
  it('are authorised with the owner’s browser wallet', async () => {
    let keys: Schemas['SessionKeys'] = {}
    let started: unknown
    let signed: unknown

    server.use(
      signedIn,
      approvalsGranted,
      http.get('/api/v1/wallets', () =>
        HttpResponse.json({ wallets: [wallet()], takesSessionKeys: true }),
      ),
      http.get('/api/v1/wallets/w-1/session-key', () => HttpResponse.json(keys)),
      http.post('/api/v1/wallets/w-1/session-key', async ({ request }) => {
        started = await request.json()
        return HttpResponse.json({ key: sessionKey(), typedData })
      }),
      http.post('/api/v1/wallets/w-1/session-key/signature', async ({ request }) => {
        signed = await request.json()
        keys = { pending: sessionKey({ state: 'authorizing' }) }
        return HttpResponse.json(keys.pending)
      }),
    )
    const calls = browserWallet(owner.toLowerCase())
    const { user } = renderApp('/wallets')

    await user.click(await screen.findByRole('button', { name: 'Authorise a session key' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(await dialog.findByText('Signs with Fake Wallet.')).toBeInTheDocument()
    await user.click(dialog.getByRole('button', { name: 'Connect and sign' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(started).toEqual({ owner: owner.toLowerCase() })
    expect(signed).toEqual({ signature: '0x5ign' })
    expect(calls).toEqual([
      { method: 'eth_requestAccounts' },
      { method: 'wallet_switchEthereumChain', params: [{ chainId: '0x89' }] },
      { method: 'eth_signTypedData_v4', params: [owner.toLowerCase(), JSON.stringify(typedData)] },
    ])

    expect(
      await screen.findByText('A session key is being authorised on Polygon, in a minute or so.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Authorise a session key' })).toBeDisabled()
  })

  it('need a browser wallet', async () => {
    server.use(
      signedIn,
      approvalsGranted,
      http.get('/api/v1/wallets', () =>
        HttpResponse.json({ wallets: [wallet()], takesSessionKeys: true }),
      ),
      http.get('/api/v1/wallets/w-1/session-key', () => HttpResponse.json({})),
    )
    const { user } = renderApp('/wallets')

    await user.click(await screen.findByRole('button', { name: 'Authorise a session key' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(await dialog.findByText(/No browser wallet found/)).toBeInTheDocument()
    expect(dialog.getByRole('button', { name: 'Connect and sign' })).toBeDisabled()
  })

  it('say what stopped them', async () => {
    let signing = false

    server.use(
      signedIn,
      approvalsGranted,
      http.get('/api/v1/wallets', () =>
        HttpResponse.json({ wallets: [wallet()], takesSessionKeys: true }),
      ),
      http.get('/api/v1/wallets/w-1/session-key', () => HttpResponse.json({})),
      http.post('/api/v1/wallets/w-1/session-key', () =>
        signing
          ? HttpResponse.json({ key: sessionKey(), typedData })
          : apiError(400, 'not_deposit_wallet', "the wallet is not this account's Deposit Wallet"),
      ),
    )
    browserWallet(owner, { eth_signTypedData_v4: declined })
    const { user } = renderApp('/wallets')

    await user.click(await screen.findByRole('button', { name: 'Authorise a session key' }))
    const dialog = within(await screen.findByRole('dialog'))
    await dialog.findByText('Signs with Fake Wallet.')
    await user.click(dialog.getByRole('button', { name: 'Connect and sign' }))
    expect(
      await dialog.findByText("The wallet is not this account's Deposit Wallet"),
    ).toBeInTheDocument()

    signing = true
    await user.click(dialog.getByRole('button', { name: 'Connect and sign' }))
    expect(await dialog.findByText('You declined in your browser wallet.')).toBeInTheDocument()
  })

  it('trade for the wallet, and only removing them ends that', async () => {
    server.use(
      signedIn,
      approvalsGranted,
      http.get('/api/v1/wallets', () =>
        HttpResponse.json({
          wallets: [wallet({ hasCredentials: true, hasKey: true, hasSessionKey: true })],
          takesKeys: true,
          takesSessionKeys: true,
        }),
      ),
      http.get('/api/v1/wallets/w-1/credentials', () =>
        HttpResponse.json({
          apiKeyHint: 'abcd…wxyz',
          signer: '0x70997970C51812dc3A010C7d01b50e0d17dc79C8',
          signatureType: 3,
          hasKey: true,
          sessionKey: true,
          createdAt: '2026-10-19T12:00:00Z',
          updatedAt: '2026-10-19T12:00:00Z',
        }),
      ),
      http.get('/api/v1/wallets/w-1/session-key', () =>
        HttpResponse.json({
          active: sessionKey({ state: 'active' }),
          pending: sessionKey({ state: 'failed', error: 'not confirmed in 15 minutes' }),
        }),
      ),
    )
    renderApp('/wallets')

    expect(await screen.findByText('Can trade')).toBeInTheDocument()
    expect(await screen.findByText(/Session key and/)).toBeInTheDocument()
    expect(screen.getByText(/places and cancels orders/)).toBeInTheDocument()
    expect(screen.getByText(/failed: not confirmed in 15 minutes/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Renew session key' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Remove session key' })).toBeInTheDocument()

    for (const name of ['Replace credentials', 'Remove credentials', 'Replace private key']) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument()
    }
  })

  describe('removed', () => {
    let revoked: unknown
    let removed: boolean
    let startedRevocation: boolean

    const active = () => {
      revoked = undefined
      removed = false
      startedRevocation = false

      server.use(
        signedIn,
        approvalsGranted,
        approvalsGranted,
        http.get('/api/v1/wallets', () =>
          HttpResponse.json({
            wallets: [
              wallet({ hasCredentials: !removed, hasKey: !removed, hasSessionKey: !removed }),
            ],
            takesSessionKeys: true,
          }),
        ),
        http.get('/api/v1/wallets/w-1/credentials', () => apiError(404, 'not_found', 'none')),
        http.get('/api/v1/wallets/w-1/session-key', () =>
          HttpResponse.json(removed ? {} : { active: sessionKey({ state: 'active' }) }),
        ),
        http.post('/api/v1/wallets/w-1/session-key/revocation', () => {
          startedRevocation = true
          return HttpResponse.json({ key: sessionKey({ state: 'active' }), typedData })
        }),
        http.post('/api/v1/wallets/w-1/session-key/revocation/signature', async ({ request }) => {
          revoked = await request.json()
          removed = true
          return HttpResponse.json({ revoked: true })
        }),
        http.delete('/api/v1/wallets/w-1/session-key', () => {
          removed = true
          return new HttpResponse(null, { status: 204 })
        }),
      )
    }

    const open = async () => {
      const { user } = renderApp('/wallets')
      await user.click(await screen.findByRole('button', { name: 'Remove session key' }))
      const dialog = within(await screen.findByRole('dialog'))
      await dialog.findByText('Signs with Fake Wallet.')

      return { user, dialog }
    }

    it('revoked with the owner’s signature', async () => {
      active()
      browserWallet(owner)
      const { user, dialog } = await open()

      await user.click(dialog.getByRole('button', { name: 'Revoke and remove' }))

      expect(await screen.findByText('Session key revoked and removed')).toBeInTheDocument()
      expect(revoked).toEqual({ signature: '0x5ign' })
      expect(
        await screen.findByRole('button', { name: 'Authorise a session key' }),
      ).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: 'Remove session key' })).not.toBeInTheDocument()
    })

    it('without revoking, when the owner declines to sign (D159)', async () => {
      active()
      browserWallet(owner, { eth_signTypedData_v4: declined })
      const { user, dialog } = await open()

      await user.click(dialog.getByRole('button', { name: 'Revoke and remove' }))

      expect(
        await screen.findByText(/Session key removed, not revoked: you declined to sign/),
      ).toBeInTheDocument()
      expect(removed).toBe(true)
      expect(revoked).toBeUndefined()
    })

    it('not signed by another account', async () => {
      active()
      browserWallet('0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC')
      const { user, dialog } = await open()

      await user.click(dialog.getByRole('button', { name: 'Revoke and remove' }))

      expect(await dialog.findByText(/switch it to 0xf39F…2266, the owner/)).toBeInTheDocument()
      expect(startedRevocation).toBe(false)
      expect(removed).toBe(false)
    })

    it('without revoking, by choice', async () => {
      active()
      browserWallet(owner)
      const { user, dialog } = await open()

      await user.click(dialog.getByRole('button', { name: 'Remove without revoking' }))

      expect(
        await screen.findByText(/Session key removed, not revoked: you chose not to sign/),
      ).toBeInTheDocument()
      expect(startedRevocation).toBe(false)
    })
  })
})
