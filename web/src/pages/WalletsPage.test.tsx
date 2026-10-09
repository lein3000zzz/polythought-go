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
  createdAt: '2026-10-01T10:00:00Z',
  ...overrides,
})

const credentials: Schemas['Credentials'] = {
  apiKeyHint: 'abcd…wxyz',
  signer: address,
  signatureType: 0,
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
