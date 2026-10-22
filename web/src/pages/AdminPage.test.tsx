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

function admin(
  users: Schemas['User'][],
  invites: Schemas['Invite'][] = [],
  codeAccess: 'everyone' | 'chosen' | 'off' = 'everyone',
  byEmail = false,
) {
  const access: Schemas['Setting'] = {
    key: 'code.access',
    group: 'Code',
    label: 'Who may write code',
    description: 'Who may write code.',
    kind: 'enum',
    options: ['everyone', 'chosen', 'off'],
    secret: false,
    isSet: codeAccess !== 'everyone',
    apply: 'live',
    value: codeAccess,
    default: 'everyone',
    source: 'database',
    env: 'POLYTHOUGHT_CODE_ACCESS',
    pending: false,
  }

  server.use(
    asAdmin,
    registration('invite'),
    http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    http.get('/api/v1/admin/users', () => HttpResponse.json({ users })),
    http.get('/api/v1/admin/invites', () => HttpResponse.json({ invites, byEmail })),
    http.get('/api/v1/admin/settings', () =>
      HttpResponse.json({ settings: [access], restartPending: false }),
    ),
  )
}

// The first cell names the user; the code switch's label names them too.
const userRow = (email: string) =>
  screen.getAllByRole('cell', { name: new RegExp(email) })[0].closest('tr')!

describe('admin page', () => {
  it('is for admins only', async () => {
    server.use(
      signedIn,
      http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    )
    const { user } = renderApp('/admin')

    expect(await screen.findByText('This page is for admins.')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Account' }))
    expect(await screen.findByRole('menuitem', { name: 'Settings' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: 'Admin' })).not.toBeInTheDocument()
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

    await screen.findByText('alice@example.com')
    expect(within(userRow('root@example.com')).getByText('You')).toBeInTheDocument()
    // Their own caps only: no role or disabling for themselves.
    expect(
      within(userRow('root@example.com'))
        .getAllByRole('button')
        .map((b) => b.getAttribute('aria-label')),
    ).toEqual(['Live caps of root@example.com', 'Agent caps of root@example.com'])

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

  it("caps a user's live buys over the instance's, and shows whose live trading stopped (D222)", async () => {
    const sent: unknown[] = []
    const cap = (key: string, label: string, value: number): Schemas['Setting'] => ({
      key,
      group: 'Trading',
      label,
      description: label,
      kind: 'int',
      secret: false,
      isSet: false,
      apply: 'live',
      value,
      default: value,
      source: 'default',
      env: '',
      pending: false,
    })

    admin([root, { ...alice, liveStopped: true, liveStoppedAt: '2026-10-20T10:00:00Z' }])
    server.use(
      http.get('/api/v1/admin/settings', () =>
        HttpResponse.json({
          settings: [
            cap('trading.max_live_order', 'Max $ a live order', 100),
            cap('trading.max_live_day', 'Max $ a day of live buys', 1000),
          ],
          restartPending: false,
        }),
      ),
      http.patch('/api/v1/admin/users/u-1', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ ...alice, liveCaps: { perOrder: 250 } })
      }),
    )
    const { user } = renderApp('/admin')

    await screen.findByText('alice@example.com')
    const row = userRow('alice@example.com')
    expect(within(row).getByText('Live stopped')).toBeInTheDocument()

    await user.click(within(row).getByRole('button', { name: 'Live caps of alice@example.com' }))
    expect(screen.getByLabelText('Max $ a live order')).toHaveAttribute(
      'placeholder',
      'Default: $100',
    )
    expect(screen.getByLabelText('Max $ a day of live buys')).toHaveAttribute(
      'placeholder',
      'Default: $1,000',
    )

    await user.type(screen.getByLabelText('Max $ a live order'), '250')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Live caps of alice@example.com saved')).toBeInTheDocument()
    expect(sent).toEqual([{ liveCaps: { perOrder: 250 } }])
  })

  it("sets a user's agent caps, beside their spend this month (D271, D277)", async () => {
    const sent: unknown[] = []
    const setting = (
      key: string,
      kind: 'int' | 'string',
      value: number | string,
    ): Schemas['Setting'] => ({
      key,
      group: 'AI agents',
      label: key,
      description: key,
      kind,
      secret: false,
      isSet: false,
      apply: 'live',
      value,
      default: value,
      source: 'default',
      env: '',
      pending: false,
    })

    admin([root, alice])
    server.use(
      http.get('/api/v1/admin/settings', () =>
        HttpResponse.json({
          settings: [
            setting('agents.max_dollars_month', 'string', '20.00'),
            setting('agents.max_input_month', 'int', 0),
            setting('agents.max_output_month', 'int', 0),
            setting('agents.max_total_month', 'int', 20_000_000),
          ],
          restartPending: false,
        }),
      ),
      http.get('/api/v1/admin/users/u-1/agent-spend', () =>
        HttpResponse.json({
          month: '2026-10-01T00:00:00Z',
          resets: '2026-11-01T00:00:00Z',
          inputTokens: 1200,
          outputTokens: 300,
          totalTokens: 1500,
          dollars: '4.20',
          calls: 7,
          costedCalls: 2,
          caps: {},
          own: {},
          killStopsAgents: false,
        }),
      ),
      http.patch('/api/v1/admin/users/u-1', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ ...alice, agentCaps: { dollars: '12.50', outputTokens: 0 } })
      }),
    )
    const { user } = renderApp('/admin')

    await screen.findByText('alice@example.com')
    const row = userRow('alice@example.com')
    expect(
      within(row).getByRole('button', { name: 'Agent caps of alice@example.com' }),
    ).toHaveTextContent('Default')

    await user.click(within(row).getByRole('button', { name: 'Agent caps of alice@example.com' }))
    expect(
      await screen.findByText(
        'This month: $4.20 (2 of 7 calls priced), 1,500 tokens (1,200 in, 300 out).',
      ),
    ).toBeInTheDocument()
    expect(screen.getByLabelText('Max $ a month')).toHaveAttribute('placeholder', 'Default: $20.00')
    expect(screen.getByLabelText('Max tokens a month')).toHaveAttribute(
      'placeholder',
      'Default: 20,000,000',
    )
    expect(screen.getByLabelText('Max input tokens a month')).toHaveAttribute(
      'placeholder',
      'Default: no cap',
    )

    await user.type(screen.getByLabelText('Max $ a month'), '12.50')
    await user.type(screen.getByLabelText('Max output tokens a month'), '0')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Agent caps of alice@example.com saved')).toBeInTheDocument()
    expect(sent).toEqual([{ agentCaps: { dollars: '12.50', outputTokens: 0 } }])
  })

  it('marks an unverified address verified, or sends its link again', async () => {
    const bob: Schemas['User'] = {
      ...alice,
      id: 'u-2',
      email: 'bob@example.com',
      emailVerified: false,
    }
    const calls: string[] = []

    admin([root, alice, bob])
    server.use(
      http.patch('/api/v1/admin/users/u-2', async ({ request }) => {
        calls.push(`patch ${JSON.stringify(await request.json())}`)
        return HttpResponse.json({ ...bob, emailVerified: true })
      }),
      http.post('/api/v1/admin/users/u-2/verification', () => {
        calls.push('link')
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/admin')

    await screen.findByText('bob@example.com')
    expect(within(userRow('alice@example.com')).queryByText('Unverified')).not.toBeInTheDocument()

    const row = within(userRow('bob@example.com'))
    expect(row.getByText('Unverified')).toBeInTheDocument()

    await user.click(row.getByRole('button', { name: 'Send link' }))
    expect(
      await screen.findByText('A new link is on its way to bob@example.com'),
    ).toBeInTheDocument()

    await user.click(row.getByRole('button', { name: 'Mark verified' }))
    expect(await screen.findByText('bob@example.com is verified')).toBeInTheDocument()
    expect(calls).toEqual(['link', 'patch {"emailVerified":true}'])
  })

  it('picks the users who may write code, while code is for chosen users', async () => {
    const sent: unknown[] = []
    admin([root, alice], [], 'chosen')
    server.use(
      http.patch('/api/v1/admin/users/u-1', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ ...alice, codeChosen: true, mayWriteCode: true })
      }),
    )
    const { user } = renderApp('/admin')

    expect(
      await screen.findByText(/Admins may write code, and the users switched on below\./),
    ).toBeInTheDocument()
    await screen.findByText('alice@example.com')
    expect(within(userRow('root@example.com')).getByText('Yes')).toBeInTheDocument()
    expect(within(userRow('root@example.com')).queryByRole('switch')).not.toBeInTheDocument()

    const chosen = within(userRow('alice@example.com')).getByRole('switch', {
      name: 'alice@example.com may write code',
    })
    expect(chosen).not.toBeChecked()

    await user.click(chosen)

    expect(await screen.findByText('alice@example.com may write code')).toBeInTheDocument()
    expect(sent).toEqual([{ codeChosen: true }])
  })

  it('says who may write code when it is not for chosen users', async () => {
    admin(
      [
        { ...root, mayWriteCode: false },
        { ...alice, codeChosen: true, mayWriteCode: false },
      ],
      [],
      'off',
    )
    renderApp('/admin')

    expect(
      await screen.findByText(/Code is off for everyone, admins included\./),
    ).toBeInTheDocument()
    await screen.findByText('alice@example.com')
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(within(userRow('alice@example.com')).getByText('No')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Config page' })).toHaveAttribute(
      'href',
      '/admin/config/code#code.access',
    )
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

  it('mails an invite to an address, and shows whom each is for (D180)', async () => {
    let sent: unknown
    let answer = { sent: true }

    admin(
      [root],
      [
        {
          id: 'i-1',
          createdBy: root.id,
          createdAt: '2026-10-01T10:00:00Z',
          expiresAt: '2099-01-01T00:00:00Z',
          email: 'old@example.com',
        },
      ],
      'everyone',
      true,
    )
    server.use(
      http.post('/api/v1/admin/invites', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json(
          {
            id: 'i-2',
            createdBy: root.id,
            createdAt: '2026-10-19T10:00:00Z',
            expiresAt: '2099-01-01T00:00:00Z',
            email: 'new@example.com',
            code: 'inv_new',
            ...answer,
          },
          { status: 201 },
        )
      }),
    )
    const { user } = renderApp('/admin')

    expect(await screen.findByText('old@example.com')).toBeInTheDocument()

    await user.type(screen.getByLabelText('Invite by email'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: 'Send invite' }))

    expect(await screen.findByText('Mailed to new@example.com')).toBeInTheDocument()
    expect(sent).toEqual({ email: 'new@example.com' })
    expect(screen.getByText(/for new@example.com alone/)).toBeInTheDocument()

    answer = { sent: false }
    await user.type(screen.getByLabelText('Invite by email'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: 'Send invite' }))
    expect(await screen.findByText(/did not go out: send this link yourself/)).toBeInTheDocument()
  })

  it('offers no mailing while invites cannot be mailed', async () => {
    admin([root])
    renderApp('/admin')

    expect(await screen.findByRole('button', { name: 'Create invite' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Invite by email')).not.toBeInTheDocument()
  })
})
