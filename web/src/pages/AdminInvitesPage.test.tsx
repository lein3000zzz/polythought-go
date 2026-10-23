import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, registration } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const root: Schemas['User'] = { ...alice, id: 'u-0', email: 'root@example.com', role: 'admin' }

function invite(id: string, extra: Partial<Schemas['Invite']> = {}): Schemas['Invite'] {
  return {
    id,
    createdBy: root.id,
    createdAt: '2026-10-01T10:00:00Z',
    expiresAt: '2099-01-01T00:00:00Z',
    maxUses: 1,
    used: 0,
    signups: [],
    status: 'open',
    ...extra,
  }
}

function invites(list: Schemas['Invite'][], byEmail = false) {
  server.use(
    http.get('/api/v1/me', () => HttpResponse.json(root)),
    registration('invite'),
    http.get('/api/v1/admin/invites', () => HttpResponse.json({ invites: list, byEmail })),
  )
}

const row = (text: string) => screen.getByText(text).closest('tr')!

describe('admin invites page (D371)', () => {
  it('is a tab of the admin pages', async () => {
    invites([])
    renderApp('/admin/invites')

    const tabs = within(await screen.findByRole('navigation', { name: 'Admin pages' }))
    expect(tabs.getByRole('link', { name: 'Invites' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('No invites yet.')).toBeInTheDocument()
    expect(screen.getByText(/People need an invite/)).toBeInTheDocument()
  })

  it('lists each code, its sign-ups of those it admits, and what became of it', async () => {
    invites([
      invite('i-1', {
        code: 'Spring-2026',
        maxUses: 5,
        used: 2,
        signups: [
          { userId: 'u-1', email: 'first@example.com', at: '2026-10-02T10:00:00Z' },
          { at: '2026-10-03T10:00:00Z' },
        ],
      }),
      invite('i-2', { code: 'inv_used', used: 1, status: 'used' }),
      invite('i-3', { status: 'expired' }),
      invite('i-4', { code: 'gone-code', status: 'revoked', revokedAt: '2026-10-04T10:00:00Z' }),
    ])
    const { user } = renderApp('/admin/invites')

    expect(await screen.findByText('1 open · 3 sign-ups through invites')).toBeInTheDocument()
    expect(within(row('Spring-2026')).getByText('Open')).toBeInTheDocument()
    expect(within(row('inv_used')).getByText('Used up')).toBeInTheDocument()
    expect(within(row('gone-code')).getByText('Revoked')).toBeInTheDocument()
    expect(within(row('Not kept')).getByText('Expired')).toBeInTheDocument() // made before codes were kept
    expect(
      within(row('gone-code')).queryByRole('button', { name: 'Revoke' }),
    ).not.toBeInTheDocument()

    await user.click(within(row('Spring-2026')).getByRole('button', { name: '2 of 5' }))
    const who = within(screen.getByRole('list', { name: 'Who signed up with Spring-2026' }))
    expect(who.getByText('first@example.com')).toBeInTheDocument()
    expect(who.getByText('A deleted account')).toBeInTheDocument()
  })

  it('makes an invite with a typed code, checked as it is typed (D393)', async () => {
    const sent: unknown[] = []
    invites([])
    server.use(
      http.post('/api/v1/admin/invites/check', async ({ request }) => {
        const { code } = (await request.json()) as { code: string }
        return HttpResponse.json(
          code.toLowerCase() === 'taken-code' ? { problem: 'Another invite has this code.' } : {},
        )
      }),
      http.post('/api/v1/admin/invites', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json(invite('i-9', { code: 'Spring-2026', maxUses: 25 }), {
          status: 201,
        })
      }),
    )
    const { user } = renderApp('/admin/invites')

    const code = await screen.findByLabelText('Code')
    await user.type(code, 'TAKEN-code')
    expect(await screen.findByRole('alert')).toHaveTextContent('Another invite has this code.')
    expect(code).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('button', { name: 'Make invite' })).toBeDisabled()

    await user.clear(code)
    await user.type(code, 'Spring-2026')
    expect(await screen.findByText('Free to take.')).toBeInTheDocument()

    await user.clear(screen.getByLabelText('Sign-ups'))
    await user.type(screen.getByLabelText('Sign-ups'), '25')
    await user.type(screen.getByLabelText('Expires'), '2026-11-30')
    await user.click(screen.getByRole('button', { name: 'Make invite' }))

    expect(await screen.findByText('Invite made: share its link')).toBeInTheDocument()
    expect(
      screen.getByText('http://localhost:3000/register?invite=Spring-2026'),
    ).toBeInTheDocument()
    expect(screen.getByText(/It admits 25 sign-ups/)).toBeInTheDocument()
    expect(sent).toEqual([
      {
        code: 'Spring-2026',
        maxUses: 25,
        expiresAt: new Date('2026-11-30T23:59:59').toISOString(),
      },
    ])
  })

  it('has a code made when none is typed, one use, for Invite lifetime', async () => {
    const sent: unknown[] = []
    invites([])
    server.use(
      http.post('/api/v1/admin/invites', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json(invite('i-9', { code: 'inv_abc+1' }), { status: 201 })
      }),
    )
    const { user } = renderApp('/admin/invites')

    await user.click(await screen.findByRole('button', { name: 'Make invite' }))
    expect(
      await screen.findByText('http://localhost:3000/register?invite=inv_abc%2B1'),
    ).toBeInTheDocument()
    expect(sent).toEqual([{ maxUses: 1 }])
  })

  it('mails an invite to an address, for it alone, once (D180)', async () => {
    let sent: unknown
    let answer = { sent: true }

    invites([invite('i-1', { email: 'old@example.com' })], true)
    server.use(
      http.post('/api/v1/admin/invites', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json(
          invite('i-2', { email: 'new@example.com', code: 'inv_new', ...answer }),
          { status: 201 },
        )
      }),
    )
    const { user } = renderApp('/admin/invites')

    expect(await screen.findByText('old@example.com')).toBeInTheDocument()

    await user.type(screen.getByLabelText('Invite by email'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: 'Send invite' }))

    expect(await screen.findByText('Mailed to new@example.com')).toBeInTheDocument()
    expect(sent).toEqual({ email: 'new@example.com' })
    expect(screen.getByText(/new@example.com alone/)).toBeInTheDocument()

    answer = { sent: false }
    await user.type(screen.getByLabelText('Invite by email'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: 'Send invite' }))
    expect(await screen.findByText(/did not go out: send this link yourself/)).toBeInTheDocument()
  })

  it('offers no mailing while invites cannot be mailed', async () => {
    invites([])
    renderApp('/admin/invites')

    expect(await screen.findByRole('button', { name: 'Make invite' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Invite by email')).not.toBeInTheDocument()
  })

  it('revokes an open invite after asking', async () => {
    let revoked = ''
    invites([invite('i-1', { code: 'Spring-2026' })])
    server.use(
      http.post('/api/v1/admin/invites/:id/revoke', ({ params }) => {
        revoked = params.id as string
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/admin/invites')

    await user.click(
      within(await screen.findByText('Spring-2026').then((c) => c.closest('tr')!)).getByRole(
        'button',
        { name: 'Revoke' },
      ),
    )
    const asked = await screen.findByRole('alertdialog', { name: 'Revoke this invite?' })
    expect(within(asked).getByText(/keep their accounts/)).toBeInTheDocument()
    await user.click(within(asked).getByRole('button', { name: 'Revoke' }))

    await waitFor(() => expect(revoked).toBe('i-1'))
    expect(await screen.findByText('Invite revoked')).toBeInTheDocument()
  })

  it('shows the requests to join waiting, approves one by mailing an invite, and denies one (D394)', async () => {
    let decided = ''
    const request = (id: string, email: string, extra: Partial<Schemas['JoinRequest']> = {}) => ({
      id,
      email,
      createdAt: '2026-10-20T09:00:00Z',
      confirmedAt: '2026-10-20T09:05:00Z',
      status: 'pending' as const,
      ...extra,
    })
    invites([])
    server.use(
      registration('invite', 12, true),
      http.get('/api/v1/admin/requests', () =>
        HttpResponse.json({
          requests: [
            request('r-1', 'new@example.com', { note: 'I trade weather markets.' }),
            request('r-2', 'spam@example.com'),
            request('r-3', 'old@example.com', {
              status: 'denied',
              decidedAt: '2026-10-19T09:00:00Z',
            }),
          ],
          pending: 2,
        }),
      ),
      http.post('/api/v1/admin/requests/:id/approve', ({ params }) => {
        decided = `approved ${params.id as string}`
        return HttpResponse.json(
          invite('i-9', { email: 'new@example.com', code: 'inv_new', sent: true }),
          { status: 201 },
        )
      }),
      http.post('/api/v1/admin/requests/:id/deny', ({ params }) => {
        decided = `denied ${params.id as string}`
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/admin/invites')

    // Counted on the tab, so they are seen from any admin page.
    const tabs = within(await screen.findByRole('navigation', { name: 'Admin pages' }))
    expect(
      await tabs.findByRole('link', { name: 'Invites, 2 requests waiting' }),
    ).toBeInTheDocument()

    const requests = within(await screen.findByRole('list', { name: 'Requests to join' }))
    expect(requests.getByText('I trade weather markets.')).toBeInTheDocument()
    expect(requests.getByText('Denied')).toBeInTheDocument()
    expect(
      requests.queryByRole('button', { name: 'Approve old@example.com' }),
    ).not.toBeInTheDocument()

    await user.click(requests.getByRole('button', { name: 'Approve new@example.com' }))
    expect(await screen.findByText('Mailed to new@example.com')).toBeInTheDocument()
    expect(decided).toBe('approved r-1')

    await user.click(requests.getByRole('button', { name: 'Deny spam@example.com' }))
    const asked = await screen.findByRole('alertdialog', { name: 'Deny this request?' })
    await user.click(within(asked).getByRole('button', { name: 'Deny' }))
    await waitFor(() => expect(decided).toBe('denied r-2'))
  })

  it('says requests are off, with none made, by hiding them', async () => {
    invites([])
    renderApp('/admin/invites')

    expect(await screen.findByText('No invites yet.')).toBeInTheDocument()
    expect(screen.queryByText('Requests to join')).not.toBeInTheDocument()
  })
})
