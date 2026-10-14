import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, apiError, registration, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const root: Schemas['User'] = { ...alice, id: 'u-0', email: 'root@example.com', role: 'admin' }

function setting(overrides: Partial<Schemas['Setting']> & Pick<Schemas['Setting'], 'key'>) {
  return {
    group: 'Accounts',
    label: overrides.key,
    description: 'What it does.',
    kind: 'string',
    secret: false,
    isSet: false,
    apply: 'live',
    value: '',
    default: '',
    source: 'default',
    env: 'POLYTHOUGHT_X',
    pending: false,
    ...overrides,
  } satisfies Schemas['Setting']
}

const registrationMode = setting({
  key: 'auth.registration',
  label: 'Registration',
  kind: 'enum',
  options: ['open', 'invite', 'closed'],
  value: 'open',
  default: 'invite',
  source: 'environment',
  env: 'POLYTHOUGHT_REGISTRATION',
})
const idle = setting({
  key: 'auth.session_idle_ttl',
  label: 'Idle sign-out',
  kind: 'duration',
  value: '24h',
  default: '168h',
  source: 'database',
})
const maxWorkflows = setting({
  key: 'workflows.max_per_user',
  group: 'Workflows',
  label: 'Workflows per user',
  kind: 'int',
  value: 100,
  default: 100,
})
const proxies = setting({
  key: 'http.trusted_proxies',
  group: 'Network',
  label: 'Trusted proxies',
  kind: 'list',
  value: ['10.0.0.0/8'],
  default: [],
  source: 'database',
})
const allowPrivate = setting({
  key: 'webhook.allow_private',
  group: 'Network',
  label: 'Webhooks to private addresses',
  kind: 'bool',
  value: false,
  default: false,
})
const logFormat = setting({
  key: 'log.format',
  group: 'Logging',
  label: 'Log format',
  kind: 'enum',
  options: ['text', 'json'],
  value: 'text',
  default: 'text',
  apply: 'restart',
})
const token = setting({
  key: 'telegram.token',
  group: 'Telegram',
  label: 'Bot token',
  secret: true,
  isSet: true,
  value: null,
  default: null,
  source: 'database',
})

const all = [registrationMode, idle, maxWorkflows, proxies, allowPrivate, logFormat, token]

function config(settings: Schemas['Setting'][] = all, restartPending = false) {
  server.use(
    http.get('/api/v1/me', () => HttpResponse.json(root)),
    registration('invite'),
    http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    http.get('/api/v1/admin/settings', () => HttpResponse.json({ settings, restartPending })),
    http.get('/api/v1/admin/settings/history', () => HttpResponse.json({ changes: [] })),
  )
}

describe('admin config page', () => {
  it('is for admins only', async () => {
    server.use(
      signedIn,
      http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    )
    renderApp('/admin/config')

    expect(await screen.findByText('This page is for admins.')).toBeInTheDocument()
  })

  it('is a tab of the admin pages', async () => {
    config()
    server.use(
      http.get('/api/v1/admin/users', () => HttpResponse.json({ users: [root] })),
      http.get('/api/v1/admin/invites', () => HttpResponse.json({ invites: [] })),
    )
    const { user, router } = renderApp('/admin')

    const tabs = within(await screen.findByRole('navigation', { name: 'Admin pages' }))
    await user.click(tabs.getByRole('link', { name: 'Config' }))

    expect(router.state.location.pathname).toBe('/admin/config')
    expect(await screen.findByRole('heading', { name: 'Accounts' })).toBeInTheDocument()
  })

  it('shows each setting with where its value comes from', async () => {
    config()
    renderApp('/admin/config')

    for (const group of ['Accounts', 'Workflows', 'Network', 'Logging', 'Telegram']) {
      expect(await screen.findByRole('heading', { name: group })).toBeInTheDocument()
    }

    // Set by the environment: shown, but not editable here.
    expect(screen.getByLabelText('Registration')).toBeDisabled()
    expect(screen.getByLabelText('Registration')).toHaveValue('open')
    expect(screen.getByText('Set by POLYTHOUGHT_REGISTRATION')).toBeInTheDocument()

    expect(screen.getByLabelText('Idle sign-out')).toHaveValue('24h')
    // At its default: the default is the placeholder, so typing sets a value.
    expect(screen.getByLabelText('Workflows per user')).toHaveValue('')
    expect(screen.getByLabelText('Workflows per user')).toHaveAttribute(
      'placeholder',
      'Default: 100',
    )
    expect(screen.getByLabelText('Trusted proxies')).toHaveValue('10.0.0.0/8')
    expect(screen.getByText('Applies after a restart.')).toBeInTheDocument()

    // Secrets are never sent to the page.
    expect(screen.getByLabelText('Bot token')).toHaveValue('')
    expect(screen.getByLabelText('Bot token')).toHaveAttribute(
      'placeholder',
      'Saved. Type a new value to replace it.',
    )
  })

  it('saves only what changed, with resets as null', async () => {
    const sent: unknown[] = []
    config()
    server.use(
      http.patch('/api/v1/admin/settings', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ settings: all, restartPending: false })
      }),
    )
    const { user } = renderApp('/admin/config')

    await user.type(await screen.findByLabelText('Workflows per user'), '250')
    await user.click(screen.getByLabelText('Webhooks to private addresses'))
    const idleRow = screen.getByLabelText('Idle sign-out').closest('fieldset')!.parentElement!
    await user.click(within(idleRow).getByRole('button', { name: 'Reset to default' }))
    expect(screen.getByLabelText('Idle sign-out')).toHaveValue('')
    await user.type(screen.getByLabelText('Bot token'), 'new-token')

    // Typing a value back to what is saved is no change.
    await user.selectOptions(screen.getByLabelText('Log format'), 'text')

    expect(screen.getByText('4 unsaved changes')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Settings saved')).toBeInTheDocument()
    expect(sent).toEqual([
      {
        values: {
          'workflows.max_per_user': 250,
          'webhook.allow_private': true,
          'auth.session_idle_ttl': null,
          'telegram.token': 'new-token',
        },
      },
    ])
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
  })

  it('shows the server’s problems on their settings', async () => {
    config()
    server.use(
      http.patch('/api/v1/admin/settings', () =>
        HttpResponse.json(
          {
            error: {
              code: 'invalid_input',
              message: 'invalid input: must be between 1 and 100000',
              problems: [
                { param: 'workflows.max_per_user', message: 'must be between 1 and 100000' },
              ],
            },
          },
          { status: 400 },
        ),
      ),
    )
    const { user } = renderApp('/admin/config')

    await user.type(await screen.findByLabelText('Workflows per user'), '0')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('must be between 1 and 100000')).toBeInTheDocument()
    expect(screen.getByLabelText('Workflows per user')).toHaveAttribute('aria-invalid', 'true')

    await user.click(screen.getByRole('button', { name: 'Discard' }))
    expect(screen.queryByText('must be between 1 and 100000')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Workflows per user')).toHaveValue('')
  })

  it('says when saving failed for another reason', async () => {
    config()
    server.use(http.patch('/api/v1/admin/settings', () => apiError(403, 'forbidden', 'forbidden')))
    const { user } = renderApp('/admin/config')

    await user.type(await screen.findByLabelText('Workflows per user'), '5')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Forbidden')).toBeInTheDocument()
  })

  it('restarts the server and waits for it to come back', async () => {
    const pending = { ...logFormat, value: 'json', source: 'database', pending: true } as const
    let restarted = false
    config([idle, pending], true)
    server.use(
      http.get('/api/v1/admin/settings', () =>
        HttpResponse.json({
          settings: [idle, { ...pending, pending: !restarted }],
          restartPending: !restarted,
        }),
      ),
      http.post('/api/v1/admin/restart', () => {
        restarted = true
        return new HttpResponse(null, { status: 202 })
      }),
    )
    const { user } = renderApp('/admin/config')

    expect(await screen.findByText('A restart is needed')).toBeInTheDocument()
    expect(screen.getByText(/Changes to Log format take effect/)).toBeInTheDocument()
    expect(screen.getByText('Waiting for a restart')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Restart now' }))
    const dialog = within(await screen.findByRole('alertdialog'))
    await user.click(dialog.getByRole('button', { name: 'Restart' }))

    expect(await screen.findByText('Server restarted', {}, { timeout: 3000 })).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('A restart is needed')).not.toBeInTheDocument())
    expect(restarted).toBe(true)
  })

  it('lists changes, newest first', async () => {
    config()
    server.use(
      http.get('/api/v1/admin/settings/history', () =>
        HttpResponse.json({
          changes: [
            {
              id: 'a-3',
              key: 'telegram.token',
              oldValue: null,
              newValue: null,
              secret: true,
              actorEmail: 'root@example.com',
              at: '2026-10-14T10:00:00Z',
            },
            {
              id: 'a-2',
              key: 'http.trusted_proxies',
              oldValue: [],
              newValue: ['10.0.0.0/8', 'fd00::/8'],
              secret: false,
              at: '2026-10-13T10:00:00Z',
            },
          ],
        }),
      ),
    )
    renderApp('/admin/config')

    const rows = within(await screen.findByRole('table')).getAllByRole('row')
    expect(rows[1]).toHaveTextContent('Bot token')
    expect(rows[1]).toHaveTextContent('Replaced or cleared')
    expect(rows[1]).toHaveTextContent('root@example.com')
    expect(rows[2]).toHaveTextContent('Trusted proxies')
    expect(rows[2]).toHaveTextContent('none → 10.0.0.0/8, fd00::/8')
    expect(rows[2]).toHaveTextContent('A deleted account')
  })
})
