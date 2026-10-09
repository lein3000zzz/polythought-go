import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, apiError, registration, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const root: Schemas['User'] = { ...alice, id: 'u-0', email: 'root@example.com', role: 'admin' }
const asAdmin = http.get('/api/v1/me', () => HttpResponse.json(root))

function admin(users: Schemas['User'][], invites: Schemas['Invite'][] = []) {
  server.use(
    asAdmin,
    registration('invite'),
    http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    http.get('/api/v1/admin/users', () => HttpResponse.json({ users })),
    http.get('/api/v1/admin/invites', () => HttpResponse.json({ invites })),
  )
}

const userRow = (email: string) =>
  screen.getByRole('cell', { name: new RegExp(email) }).closest('tr')!

describe('admin page', () => {
  it('is for admins only', async () => {
    server.use(
      signedIn,
      http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    )
    renderApp('/admin')

    expect(await screen.findByText('This page is for admins.')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Admin' })).not.toBeInTheDocument()
  })

  it('creates users and disables them after asking', async () => {
    const sent: unknown[] = []
    admin([root, alice])
    server.use(
      http.post('/api/v1/admin/users', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ ...alice, id: 'u-2', email: 'bob@example.com' }, { status: 201 })
      }),
      http.patch('/api/v1/admin/users/u-1', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ ...alice, disabled: true })
      }),
    )
    const { user } = renderApp('/admin')

    expect(await screen.findByRole('link', { name: 'Admin' })).toBeInTheDocument()
    await screen.findByText('alice@example.com')
    expect(within(userRow('root@example.com')).getByText('You')).toBeInTheDocument()
    expect(within(userRow('root@example.com')).queryByRole('button')).not.toBeInTheDocument()

    await user.type(screen.getByLabelText('Email'), 'bob@example.com')
    await user.type(screen.getByLabelText('Password'), 'a long enough password')
    await user.selectOptions(screen.getByLabelText('Role'), 'admin')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Created bob@example.com')).toBeInTheDocument()

    await user.click(within(userRow('alice@example.com')).getByRole('button', { name: 'Disable' }))
    const dialog = within(await screen.findByRole('alertdialog'))
    expect(dialog.getByText(/workflows are turned off and stay off/)).toBeInTheDocument()
    await user.click(dialog.getByRole('button', { name: 'Disable' }))

    await waitFor(() => expect(sent).toHaveLength(2))
    expect(sent).toEqual([
      { email: 'bob@example.com', password: 'a long enough password', role: 'admin' },
      { disabled: true },
    ])
  })

  it('says why the last admin cannot be demoted', async () => {
    const other: Schemas['User'] = { ...root, id: 'u-9', email: 'other@example.com' }
    admin([root, other])
    server.use(
      http.patch('/api/v1/admin/users/u-9', () =>
        apiError(409, 'last_admin', 'the last admin cannot be demoted or disabled'),
      ),
    )
    const { user } = renderApp('/admin')

    await user.click(
      within(await waitFor(() => userRow('other@example.com'))).getByRole('button', {
        name: 'Make user',
      }),
    )
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Make user' }),
    )

    expect(
      await screen.findByText('The last admin cannot be demoted or disabled'),
    ).toBeInTheDocument()
  })

  it('creates invites as links and shows what became of them', async () => {
    const invite = (id: string, extra: Partial<Schemas['Invite']>): Schemas['Invite'] => ({
      id,
      createdBy: root.id,
      createdAt: '2026-10-01T10:00:00Z',
      expiresAt: '2099-01-01T00:00:00Z',
      ...extra,
    })

    admin(
      [root],
      [
        invite('i-1', {}),
        invite('i-2', { usedBy: alice.id, usedAt: '2026-10-02T10:00:00Z' }),
        invite('i-3', { expiresAt: '2020-01-01T00:00:00Z' }),
      ],
    )
    server.use(
      http.post('/api/v1/admin/invites', () =>
        HttpResponse.json(invite('i-4', { code: 'inv_abc+1' }), { status: 201 }),
      ),
    )
    const { user } = renderApp('/admin')

    expect(await screen.findByText(/People need an invite/)).toBeInTheDocument()
    expect(screen.getByText('Open')).toBeInTheDocument()
    expect(screen.getByText(/^Used /)).toBeInTheDocument()
    expect(screen.getByText('Expired')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Create invite' }))
    expect(
      await screen.findByText('http://localhost:3000/register?invite=inv_abc%2B1'),
    ).toBeInTheDocument()
  })
})
