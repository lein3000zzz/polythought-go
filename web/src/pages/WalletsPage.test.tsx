import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { apiError, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const address = '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed'

const wallet = (overrides: Partial<Schemas['Wallet']> = {}): Schemas['Wallet'] => ({
  id: 'w-1',
  address,
  label: 'Main',
  hasCredentials: false,
  hasKey: false,
  hasSessionKey: false,
  createdAt: '2026-10-01T10:00:00Z',
  ...overrides,
})

const credentials: Schemas['Credentials'] = {
  apiKeyHint: 'abcd…wxyz',
  signer: address,
  signatureType: 0,
  hasKey: false,
  sessionKey: false,
  createdAt: '2026-10-02T10:00:00Z',
  updatedAt: '2026-10-02T10:00:00Z',
}

describe('wallets page', () => {
  it('links a wallet', async () => {
    let linked: unknown
    let wallets: Schemas['Wallet'][] = []

    server.use(
      signedIn,
      http.get('/api/v1/wallets', () => HttpResponse.json({ wallets })),
      http.post('/api/v1/wallets', async ({ request }) => {
        linked = await request.json()
        wallets = [wallet()]
        return HttpResponse.json(wallet(), { status: 201 })
      }),
    )
    const { user } = renderApp('/wallets')

    expect(await screen.findByText('No wallets linked yet.')).toBeInTheDocument()
    await user.type(screen.getByLabelText('Address'), address)
    await user.type(screen.getByLabelText('Label'), 'Main')
    await user.click(screen.getByRole('button', { name: 'Link' }))

    expect(await screen.findByText('No API credentials.')).toBeInTheDocument()
    expect(linked).toEqual({ address, label: 'Main' })
    expect(screen.getByLabelText('Address')).toHaveValue('')
  })

  it('stores credentials once Polymarket accepts them', async () => {
    let attempts = 0
    let sent: unknown

    server.use(
      signedIn,
      http.get('/api/v1/wallets', () =>
        HttpResponse.json({ wallets: [wallet({ hasCredentials: attempts > 1 })] }),
      ),
      http.get('/api/v1/wallets/w-1/credentials', () => HttpResponse.json(credentials)),
      http.put('/api/v1/wallets/w-1/credentials', async ({ request }) => {
        attempts++
        sent = await request.json()

        return attempts === 1
          ? apiError(400, 'credentials_rejected', 'polymarket rejected the credentials')
          : HttpResponse.json(credentials)
      }),
    )
    const { user } = renderApp('/wallets')

    await user.click(await screen.findByRole('button', { name: 'Add credentials' }))
    const dialog = within(await screen.findByRole('dialog'))
    await user.type(dialog.getByLabelText('API key'), 'key-1')
    await user.type(dialog.getByLabelText('Secret'), 'c2VjcmV0')
    await user.type(dialog.getByLabelText('Passphrase'), 'pass')
    await user.selectOptions(dialog.getByLabelText('Account type'), '2')
    await user.click(dialog.getByRole('button', { name: 'Check and save' }))

    expect(await dialog.findByText('Polymarket rejected the credentials')).toBeInTheDocument()
    expect(sent).toEqual({
      apiKey: 'key-1',
      secret: 'c2VjcmV0',
      passphrase: 'pass',
      signatureType: 2,
    })

    await user.type(dialog.getByLabelText('Signer'), ` ${address} `)
    await user.click(dialog.getByRole('button', { name: 'Check and save' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(sent).toMatchObject({ signer: address })
    expect(await screen.findByText(/API key abcd…wxyz/)).toBeInTheDocument()
  })

  it('imports a private key, so the wallet can trade', async () => {
    let imported = false
    let sent: unknown

    server.use(
      signedIn,
      http.get('/api/v1/wallets', () =>
        HttpResponse.json({
          wallets: [wallet({ hasCredentials: imported, hasKey: imported })],
          takesKeys: true,
        }),
      ),
      http.get('/api/v1/wallets/w-1/credentials', () =>
        imported
          ? HttpResponse.json({ ...credentials, hasKey: true })
          : apiError(404, 'not_found', 'none'),
      ),
      http.put('/api/v1/wallets/w-1/key', async ({ request }) => {
        sent = await request.json()
        if ((sent as { privateKey: string }).privateKey === '0xnot') {
          return apiError(400, 'key_not_for_wallet', 'this key does not control the wallet')
        }

        imported = true
        return HttpResponse.json({ ...credentials, hasKey: true })
      }),
    )
    const { user } = renderApp('/wallets')

    await user.click(await screen.findByRole('button', { name: 'Import private key' }))
    const dialog = within(await screen.findByRole('dialog'))
    const field = dialog.getByLabelText('Private key')
    expect(field).toHaveAttribute('type', 'password')

    await user.type(field, '0xnot')
    await user.click(dialog.getByRole('button', { name: 'Import' }))
    expect(await dialog.findByText('This key does not control the wallet')).toBeInTheDocument()

    await user.clear(field)
    await user.type(field, '0xgood')
    await user.click(dialog.getByRole('button', { name: 'Import' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(sent).toEqual({ privateKey: '0xgood' })
    expect(await screen.findByText('Can trade')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Replace private key' })).toBeInTheDocument()
  })

  it('offers no key import while the instance takes none', async () => {
    server.use(
      signedIn,
      http.get('/api/v1/wallets', () =>
        HttpResponse.json({ wallets: [wallet()], takesKeys: false }),
      ),
    )
    renderApp('/wallets')

    expect(await screen.findByRole('button', { name: 'Add credentials' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Import private key' })).not.toBeInTheDocument()
  })

  it('unlinks after asking', async () => {
    let unlinked = false

    server.use(
      signedIn,
      http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: unlinked ? [] : [wallet()] })),
      http.delete('/api/v1/wallets/w-1', () => {
        unlinked = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/wallets')

    await user.click(await screen.findByRole('button', { name: 'Unlink' }))
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Unlink' }),
    )

    expect(await screen.findByText('No wallets linked yet.')).toBeInTheDocument()
  })
})
