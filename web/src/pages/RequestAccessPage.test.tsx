import { screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { apiError, registration, signedOut } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

describe('asking for an invite (D394)', () => {
  it('is offered on the sign-up page while requests are open', async () => {
    server.use(signedOut, registration('invite', 12, true))
    renderApp('/register')

    expect(await screen.findByRole('link', { name: 'Ask for one' })).toHaveAttribute(
      'href',
      '/request-access',
    )
  })

  it('is not offered to someone with an invite link', async () => {
    server.use(
      signedOut,
      registration('invite', 12, true),
      http.post('/api/v1/auth/invite', () => HttpResponse.json({})),
    )
    renderApp('/register?invite=inv_1')

    expect(await screen.findByLabelText('Invite code')).toHaveValue('inv_1')
    expect(screen.queryByRole('link', { name: 'Ask for one' })).not.toBeInTheDocument()
  })

  it('says so while requests are off', async () => {
    server.use(signedOut, registration('invite'))
    renderApp('/request-access')

    expect(await screen.findByText(/takes no requests to join/)).toBeInTheDocument()
  })

  it('sends the address and the note, and says to look for the link', async () => {
    let sent: unknown
    server.use(
      signedOut,
      registration('invite', 12, true),
      http.post('/api/v1/auth/requests', async ({ request }) => {
        sent = await request.json()
        return new HttpResponse(null, { status: 202 })
      }),
    )
    const { user } = renderApp('/request-access')

    await user.type(await screen.findByLabelText('Email'), 'new@example.com')
    await user.type(screen.getByLabelText('Note (optional)'), '  I trade weather markets. ')
    expect(screen.getByText('27 of 500 characters.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Ask for an invite' }))

    expect(await screen.findByText('Check your inbox')).toBeInTheDocument()
    expect(screen.getByText(/If new@example.com can ask, a link to confirm/)).toBeInTheDocument()
    expect(sent).toEqual({ email: 'new@example.com', note: 'I trade weather markets.' })
  })

  it('confirms the request from its mailed link, once', async () => {
    const tokens: unknown[] = []
    server.use(
      signedOut,
      http.post('/api/v1/auth/requests/confirm', async ({ request }) => {
        tokens.push(await request.json())
        return new HttpResponse(null, { status: 204 })
      }),
    )
    renderApp('/request-access?token=tok')

    expect(await screen.findByText('Your request is with the admins')).toBeInTheDocument()
    await waitFor(() => expect(tokens).toEqual([{ token: 'tok' }]))
  })

  it('says when the link does not work', async () => {
    server.use(
      signedOut,
      http.post('/api/v1/auth/requests/confirm', () =>
        apiError(400, 'invalid_link', 'this link does not work any more'),
      ),
    )
    renderApp('/request-access?token=used')

    expect(await screen.findByText('This link does not work')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Ask again' })).toHaveAttribute(
      'href',
      '/request-access',
    )
  })
})
