import '@/test/browser'

import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { apiError, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const token: Schemas['APIToken'] = {
  id: 't-1',
  name: 'backtest',
  prefix: 'pt_abc',
  createdAt: '2026-10-01T10:00:00Z',
}

describe('settings page', () => {
  it('changes the password', async () => {
    const sent: unknown[] = []

    server.use(
      signedIn,
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
})
