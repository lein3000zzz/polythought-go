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
const shortestPassword = setting({
  key: 'auth.min_password_length',
  label: 'Shortest password',
  kind: 'int',
  value: 16,
  default: 12,
  source: 'file',
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

const all = [
  registrationMode,
  idle,
  shortestPassword,
  maxWorkflows,
  proxies,
  allowPrivate,
  logFormat,
  token,
]

function config(settings: Schemas['Setting'][] = all, restartPending = false) {
  server.use(
    http.get('/api/v1/me', () => HttpResponse.json(root)),
    registration('invite'),
    http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    http.get('/api/v1/admin/settings', () => HttpResponse.json({ settings, restartPending })),
    http.get('/api/v1/admin/settings/history', () => HttpResponse.json({ changes: [] })),
    http.get('/api/v1/admin/geoblock', () => HttpResponse.json({ checked: false, blocked: false })),
    http.get('/api/v1/admin/code-runners', () => HttpResponse.json(inTheServer)),
    http.get('/api/v1/admin/version', () => HttpResponse.json(devBuild)),
    http.get('/api/v1/admin/updates', () => HttpResponse.json(checkOff)),
  )
}

const checkOff: Schemas['Updates'] = {
  current: 'dev',
  on: false,
  prereleases: false,
  newer: false,
  updater: false,
}

const release012 = {
  version: '0.1.2',
  commit: '0123456789abcdef0123456789abcdef01234567',
  modified: false,
  go: 'go1.27.3',
}

const newer: Schemas['Updates'] = {
  current: '0.1.2',
  on: true,
  prereleases: false,
  latest: {
    version: '0.1.3',
    prerelease: false,
    url: 'https://github.com/lein3000zzz/polythought-go/releases/tag/v0.1.3',
    publishedAt: '2026-10-25T09:00:00Z',
  },
  newer: true,
  checkedAt: '2026-10-25T10:00:00Z',
  updater: false,
}

const devBuild: Schemas['ServerVersion'] = {
  version: 'dev',
  commit: '',
  modified: false,
  go: 'go1.27.3',
}

const inTheServer: Schemas['CodeRunners'] = {
  inRunners: false,
  size: 0,
  running: 0,
  sandboxed: false,
  why: '',
  restarts: 0,
  lastError: '',
  failed: '',
}

const runnerProcesses = setting({
  key: 'code.runners',
  group: 'Code',
  label: 'Runner processes',
  kind: 'bool',
  value: true,
  default: false,
  apply: 'restart',
})

/** The Code group, its runners as status says. */
function runners(status: Partial<Schemas['CodeRunners']>) {
  config([runnerProcesses])
  server.use(
    http.get('/api/v1/admin/code-runners', () => HttpResponse.json({ ...inTheServer, ...status })),
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
    expect(await screen.findByRole('heading', { name: 'Polymarket access' })).toBeInTheDocument()
  })

  it('shows each setting with where its value comes from, a page for each group', async () => {
    config()
    const { user } = renderApp('/admin/config/accounts')

    expect(await screen.findByRole('heading', { name: 'Accounts' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Workflows per user')).not.toBeInTheDocument()

    // Set by the environment: shown, but not editable here.
    expect(screen.getByLabelText('Registration')).toBeDisabled()
    expect(screen.getByLabelText('Registration')).toHaveValue('open')
    expect(screen.getByText('Set by POLYTHOUGHT_REGISTRATION')).toBeInTheDocument()

    expect(screen.getByLabelText('Idle sign-out')).toHaveValue('24h')
    // Set in the config file: the same, and it says where.
    expect(screen.getByLabelText('Shortest password')).toBeDisabled()
    expect(screen.getByLabelText('Shortest password')).toHaveValue('16')
    expect(screen.getByText('Set in the config file')).toBeInTheDocument()
    expect(
      screen.getByText("Change auth.min_password_length in the server's config file."),
    ).toBeInTheDocument()
    // At its default: the default is the placeholder, so typing sets a value.
    const menu = within(screen.getByRole('navigation', { name: 'Config pages' }))
    await user.click(menu.getByRole('link', { name: 'Workflows' }))
    expect(await screen.findByRole('heading', { name: 'Workflows' })).toBeInTheDocument()
    expect(screen.getByLabelText('Workflows per user')).toHaveValue('')
    expect(screen.getByLabelText('Workflows per user')).toHaveAttribute(
      'placeholder',
      'Default: 100',
    )
    await user.click(menu.getByRole('link', { name: 'Network' }))
    expect(await screen.findByLabelText('Trusted proxies')).toHaveValue('10.0.0.0/8')
    await user.click(menu.getByRole('link', { name: 'Logging' }))
    expect(await screen.findByText('Applies after a restart.')).toBeInTheDocument()

    // Secrets are never sent to the page.
    await user.click(menu.getByRole('link', { name: 'Telegram' }))
    await screen.findByLabelText('Bot token')
    expect(screen.getByLabelText('Bot token')).toHaveValue('')
    expect(screen.getByLabelText('Bot token')).toHaveAttribute(
      'placeholder',
      'Saved. Type a new value to replace it.',
    )
  })

  it('saves only what changed on its page, with resets as null', async () => {
    const sent: unknown[] = []
    config()
    server.use(
      http.patch('/api/v1/admin/settings', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ settings: all, restartPending: false })
      }),
    )
    const { user } = renderApp('/admin/config/accounts')

    const idleRow = (await screen.findByLabelText('Idle sign-out')).closest(
      'fieldset',
    )!.parentElement!
    await user.click(within(idleRow).getByRole('button', { name: 'Reset to default' }))
    expect(screen.getByLabelText('Idle sign-out')).toHaveValue('')
    expect(screen.getByText('1 unsaved change')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Settings saved')).toBeInTheDocument()
    expect(sent).toEqual([{ values: { 'auth.session_idle_ttl': null } }])
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()

    // Typing a value back to what is saved is no change; a secret typed is one.
    await user.click(screen.getByRole('link', { name: 'Logging' }))
    await user.selectOptions(await screen.findByLabelText('Log format'), 'text')
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: 'Telegram' }))
    await user.type(await screen.findByLabelText('Bot token'), 'new-token')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(sent).toHaveLength(2))
    expect(sent[1]).toEqual({ values: { 'telegram.token': 'new-token' } })
  })

  it('keeps unsaved changes across its pages, marked in the menu, each page saving its own (D391)', async () => {
    const sent: unknown[] = []
    config()
    server.use(
      http.patch('/api/v1/admin/settings', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ settings: all, restartPending: false })
      }),
    )
    const { user } = renderApp('/admin/config/workflows')
    const menu = within(await screen.findByRole('navigation', { name: 'Config pages' }))

    await user.type(await screen.findByLabelText('Workflows per user'), '250')
    expect(menu.getByRole('link', { name: 'Workflows, 1 unsaved' })).toBeInTheDocument()

    await user.click(menu.getByRole('link', { name: 'Network' }))
    await user.click(await screen.findByLabelText('Webhooks to private addresses'))
    expect(screen.getByText('1 unsaved change')).toBeInTheDocument() // this page's
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(sent).toEqual([{ values: { 'webhook.allow_private': true } }]))
    expect(menu.getByRole('link', { name: 'Workflows, 1 unsaved' })).toBeInTheDocument()
    expect(menu.getByRole('link', { name: 'Network' })).toBeInTheDocument()

    // Back on its page, the draft is still there to save.
    await user.click(menu.getByRole('link', { name: 'Workflows, 1 unsaved' }))
    expect(await screen.findByLabelText('Workflows per user')).toHaveValue('250')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(sent).toHaveLength(2))
    expect(sent[1]).toEqual({ values: { 'workflows.max_per_user': 250 } })
  })

  it('asks before leaving Config with unsaved changes', async () => {
    config()
    server.use(
      http.get('/api/v1/admin/users', () => HttpResponse.json({ users: [root] })),
      http.get('/api/v1/admin/invites', () => HttpResponse.json({ invites: [] })),
    )
    const { user, router } = renderApp('/admin/config/workflows')

    await user.type(await screen.findByLabelText('Workflows per user'), '250')
    const tabs = within(screen.getByRole('navigation', { name: 'Admin pages' }))
    await user.click(tabs.getByRole('link', { name: 'Users' }))

    const asked = await screen.findByRole('alertdialog', { name: 'Leave without saving?' })
    expect(
      within(asked).getByText('1 unsaved change on the Config pages will be lost.'),
    ).toBeInTheDocument()
    await user.click(within(asked).getByRole('button', { name: 'Stay' }))
    expect(router.state.location.pathname).toBe('/admin/config/workflows')
    expect(screen.getByLabelText('Workflows per user')).toHaveValue('250')

    await user.click(tabs.getByRole('link', { name: 'Users' }))
    await user.click(await screen.findByRole('button', { name: 'Leave' }))
    expect(router.state.location.pathname).toBe('/admin')
  })

  it('asks before turning live trading off, which cancels every resting live order (D226)', async () => {
    const sent: unknown[] = []
    const live = setting({
      key: 'trading.live',
      group: 'Trading',
      label: 'Live trading',
      kind: 'bool',
      value: true,
      default: false,
      source: 'database',
    })
    config([live])
    server.use(
      http.patch('/api/v1/admin/settings', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json({ settings: [{ ...live, value: false }], restartPending: false })
      }),
    )
    const { user } = renderApp('/admin/config/trading')

    await user.click(await screen.findByLabelText('Live trading'))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    const asked = await screen.findByRole('alertdialog', { name: 'Turn live trading off?' })
    expect(
      within(asked).getByText(/Every live order resting on the book is cancelled/),
    ).toBeInTheDocument()
    await user.click(within(asked).getByRole('button', { name: 'Cancel' }))
    expect(sent).toEqual([])

    await user.click(screen.getByRole('button', { name: 'Save' }))
    await user.click(await screen.findByRole('button', { name: 'Turn off and cancel' }))

    await waitFor(() => expect(sent).toEqual([{ values: { 'trading.live': false } }]))
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
    const { user } = renderApp('/admin/config/workflows')

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
    const { user } = renderApp('/admin/config/workflows')

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
    const { user } = renderApp('/admin/config/accounts')

    expect(await screen.findByText('A restart is needed')).toBeInTheDocument()
    // The banner is on every page, each setting it names a link to its own.
    expect(screen.getByText(/take effect when the server restarts/)).toHaveTextContent(
      'Changes to Log format take effect when the server restarts.',
    )
    expect(screen.getByRole('link', { name: 'Log format' })).toHaveAttribute(
      'href',
      '/admin/config/logging#log.format',
    )
    await user.click(screen.getByRole('link', { name: 'Log format' }))
    expect(await screen.findByText('Waiting for a restart')).toBeInTheDocument()
    expect(screen.getByText('A restart is needed')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Restart now' }))
    const dialog = within(await screen.findByRole('alertdialog'))
    await user.click(dialog.getByRole('button', { name: 'Restart' }))

    expect(await screen.findByText('Server restarted', {}, { timeout: 3000 })).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('A restart is needed')).not.toBeInTheDocument())
    expect(restarted).toBe(true)
  })

  it('sends a test email through the saved mail server, saying what it answered', async () => {
    const mail = (key: string, value: string) => setting({ key, group: 'Mail', value, default: '' })
    let sent: unknown
    let answer = apiError(
      502,
      'mail_failed',
      'the mail server did not take the message: 535 5.7.8 Authentication credentials invalid',
    )

    config([
      mail('mail.host', 'mail.example.com'),
      mail('mail.from_address', 'polythought@example.com'),
    ])
    server.use(
      http.post('/api/v1/admin/mail/test', async ({ request }) => {
        sent = await request.json()
        return answer
      }),
    )
    const { user } = renderApp('/admin/config/mail')

    const field = await screen.findByLabelText('Send a test email to')
    expect(field).toHaveValue(root.email)

    await user.click(screen.getByRole('button', { name: 'Send test email' }))
    expect(
      await screen.findByText(
        'The mail server did not take the message: 535 5.7.8 Authentication credentials invalid',
      ),
    ).toBeInTheDocument()
    expect(sent).toEqual({ to: root.email })

    answer = new HttpResponse(null, { status: 204 })
    await user.clear(field)
    await user.type(field, 'ops@example.com')
    await user.click(screen.getByRole('button', { name: 'Send test email' }))
    expect(await screen.findByText(/Sent to ops@example.com/)).toBeInTheDocument()
  })

  it('sends no test email before a mail server is saved', async () => {
    config([setting({ key: 'mail.host', group: 'Mail', value: '' })])
    renderApp('/admin/config/mail')

    expect(
      await screen.findByText('Save a mail server and a From address first.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send test email' })).toBeDisabled()
  })

  it("says code runs in the server's process", async () => {
    runners({})
    renderApp('/admin/config/code')

    expect(await screen.findByText("Code runs in the server's process.")).toBeInTheDocument()
  })

  it('says code runs in runner processes, sandboxed, and which restarted (D357)', async () => {
    runners({
      inRunners: true,
      size: 8,
      running: 7,
      sandboxed: true,
      restarts: 1,
      lastError: "it ran past its job's time",
    })
    renderApp('/admin/config/code')

    expect(
      await screen.findByText('Code runs in 8 runner processes, sandboxed.'),
    ).toBeInTheDocument()
    expect(
      screen.getByText('7 of them running now: the others start again with their next run.'),
    ).toBeInTheDocument()
    expect(
      screen.getByText("1 restarted since the server started: it ran past its job's time."),
    ).toBeInTheDocument()
  })

  it('warns while the runners are not sandboxed (D354)', async () => {
    runners({
      inRunners: true,
      size: 8,
      running: 8,
      why: 'nothing can be sandboxed here: only Linux has the sandbox, and this is darwin',
    })
    renderApp('/admin/config/code')

    expect(
      await screen.findByText(
        'Code runs in 8 runner processes, not sandboxed. Nothing can be sandboxed here: only Linux has the sandbox, and this is darwin.',
      ),
    ).toBeInTheDocument()
  })

  it('says when the runners would not start, so code does not run (D358)', async () => {
    runners({ inRunners: true, failed: 'coderunner: its engine: the prelude does not compile' })
    renderApp('/admin/config/code')

    expect(
      await screen.findByText(
        'The runner processes would not start, so code does not run: coderunner: its engine: the prelude does not compile',
      ),
    ).toBeInTheDocument()
  })

  it('says which release the server runs, with its notes and commit (D425)', async () => {
    config()
    server.use(
      http.get('/api/v1/admin/version', () =>
        HttpResponse.json({
          version: '0.1.2',
          commit: '0123456789abcdef0123456789abcdef01234567',
          modified: false,
          go: 'go1.27.3',
        }),
      ),
    )
    renderApp('/admin/config')

    expect(await screen.findByRole('link', { name: '0.1.2' })).toHaveAttribute(
      'href',
      'https://github.com/lein3000zzz/polythought-go/releases/tag/v0.1.2',
    )
    expect(screen.getByRole('link', { name: '0123456' })).toHaveAttribute(
      'href',
      'https://github.com/lein3000zzz/polythought-go/commit/0123456789abcdef0123456789abcdef01234567',
    )
    // the tests' app is a development build: not the server's release
    expect(
      screen.getByText(/This page's app is a development build: reload it/),
    ).toBeInTheDocument()
  })

  it('says a development build is one, and nothing of the app when it is the same', async () => {
    config()
    renderApp('/admin/config')

    expect(await screen.findByText('A development build')).toBeInTheDocument()
    expect(screen.getByText(/built with go1.27.3/)).toBeInTheDocument()
    expect(screen.queryByText(/This page's app is/)).not.toBeInTheDocument()
  })

  it('says the update check is off, linking to it', async () => {
    config()
    renderApp('/admin/config')

    expect(await screen.findByText(/The update check is off/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Check for updates' })).toHaveAttribute(
      'href',
      '/admin/config/updates#updates.check',
    )
  })

  it('says a newer release is out, and how to update by hand (D424)', async () => {
    config()
    server.use(
      http.get('/api/v1/admin/version', () => HttpResponse.json(release012)),
      http.get('/api/v1/admin/updates', () => HttpResponse.json(newer)),
    )
    renderApp('/admin/config')

    expect(await screen.findByText('Polythought 0.1.3 is out')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'See what changed' })).toHaveAttribute(
      'href',
      'https://github.com/lein3000zzz/polythought-go/releases/tag/v0.1.3',
    )
    expect(screen.getByText(/--profile app pull/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'set up the updater' })).toHaveAttribute(
      'href',
      '/docs/self-hosting/install/#updates',
    )
    expect(screen.queryByRole('button', { name: /Update to/ })).not.toBeInTheDocument()
  })

  it('checks for updates again', async () => {
    let checks = 0

    config()
    server.use(
      http.get('/api/v1/admin/version', () => HttpResponse.json(release012)),
      http.get('/api/v1/admin/updates', () =>
        HttpResponse.json({
          ...newer,
          latest: undefined,
          newer: false,
          error: 'GitHub shows no release of Polythought yet',
        }),
      ),
      http.post('/api/v1/admin/updates/check', () => {
        checks++
        return HttpResponse.json(newer)
      }),
    )
    const { user } = renderApp('/admin/config')

    const card = (await screen.findByText('GitHub shows no release of Polythought yet.')).closest(
      'section',
    )!
    await user.click(within(card).getByRole('button', { name: 'Check now' }))

    expect(await screen.findByText('Polythought 0.1.3 is out')).toBeInTheDocument()
    expect(checks).toBe(1)
  })

  it('updates the server with the updater, and waits for it (D425)', async () => {
    let asked: unknown

    config()
    server.use(
      http.get('/api/v1/admin/version', () =>
        HttpResponse.json(asked ? { ...release012, version: '0.1.3' } : release012),
      ),
      // Back, the server runs the newest release.
      http.get('/api/v1/admin/updates', () =>
        HttpResponse.json(
          asked
            ? {
                ...newer,
                current: '0.1.3',
                newer: false,
                updater: true,
                update: { state: 'done', version: '0.1.3' },
              }
            : { ...newer, updater: true, update: { state: 'idle' } },
        ),
      ),
      http.post('/api/v1/admin/updates/apply', async ({ request }) => {
        asked = await request.json()
        return new HttpResponse(null, { status: 202 })
      }),
    )
    const { user } = renderApp('/admin/config')

    await user.click(await screen.findByRole('button', { name: 'Update to 0.1.3' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/back it up first/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Update' }))

    expect(
      await screen.findByText('Updated to 0.1.3. Reload the page to use its app.', undefined, {
        timeout: 5000,
      }),
    ).toBeInTheDocument()
    expect(asked).toEqual({ version: '0.1.3' })
    expect(
      await screen.findByText(/This page's app is a development build: reload it/),
    ).toBeInTheDocument()
    expect(
      screen.getByText('Updated to 0.1.3. Reload the page to use its app.'),
    ).toBeInTheDocument()
  })

  it("says why an update failed, with the updater's log", async () => {
    let asked = false

    config()
    server.use(
      http.get('/api/v1/admin/version', () => HttpResponse.json(release012)),
      http.get('/api/v1/admin/updates', () =>
        HttpResponse.json({
          ...newer,
          updater: true,
          update: asked
            ? {
                state: 'failed',
                version: '0.1.3',
                error: 'pulling the images: docker compose: exit status 1',
                log: 'Error response from daemon: manifest unknown',
              }
            : { state: 'idle' },
        }),
      ),
      http.post('/api/v1/admin/updates/apply', () => {
        asked = true
        return new HttpResponse(null, { status: 202 })
      }),
    )
    const { user } = renderApp('/admin/config')

    await user.click(await screen.findByRole('button', { name: 'Update to 0.1.3' }))
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Update' }),
    )

    expect(
      await screen.findByText(
        'The update failed: pulling the images: docker compose: exit status 1.',
        undefined,
        {
          timeout: 5000,
        },
      ),
    ).toBeInTheDocument()
    expect(screen.getByText('Error response from daemon: manifest unknown')).toBeInTheDocument()
  })

  it('says when the updater does not answer', async () => {
    config()
    server.use(
      http.get('/api/v1/admin/version', () => HttpResponse.json(release012)),
      http.get('/api/v1/admin/updates', () =>
        HttpResponse.json({
          ...newer,
          updater: true,
          updaterError:
            'upstream service unavailable: the updater did not answer at http://updater:8080: is it running?',
        }),
      ),
    )
    renderApp('/admin/config')

    expect(
      await screen.findByText(/The updater does not answer: .* is it running\?/),
    ).toBeInTheDocument()
  })

  it('says whether Polymarket takes orders from the server, and checks again (D186)', async () => {
    let checks = 0

    config()
    server.use(
      http.get('/api/v1/admin/geoblock', () =>
        HttpResponse.json({
          checked: true,
          blocked: true,
          ip: '203.0.113.42',
          country: 'DE',
          region: 'HE',
          checkedAt: '2026-10-20T09:00:00Z',
        }),
      ),
      http.post('/api/v1/admin/geoblock/check', () => {
        checks++
        return HttpResponse.json({
          checked: true,
          blocked: false,
          ip: '198.51.100.7',
          country: 'IE',
          region: 'L',
          checkedAt: '2026-10-20T10:00:00Z',
        })
      }),
    )
    const { user } = renderApp('/admin/config')

    expect(await screen.findByText('Polymarket blocks orders from this server')).toBeInTheDocument()
    expect(screen.getByText(/It answers blocked for DE, HE, from 203.0.113.42/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'its list of regions' })).toHaveAttribute(
      'href',
      'https://docs.polymarket.com/api-reference/geoblock',
    )

    await user.click(screen.getByRole('button', { name: 'Check now' }))

    expect(
      await screen.findByText(
        'Polymarket takes orders from this server: IE, L, from 198.51.100.7.',
      ),
    ).toBeInTheDocument()
    expect(checks).toBe(1)
  })

  it('says when the check failed, keeping the answer before', async () => {
    config()
    server.use(
      http.get('/api/v1/admin/geoblock', () =>
        HttpResponse.json({
          checked: true,
          blocked: false,
          ip: '198.51.100.7',
          country: 'IE',
          checkedAt: '2026-10-20T09:00:00Z',
          error: 'upstream service unavailable',
        }),
      ),
    )
    renderApp('/admin/config')

    expect(
      await screen.findByText(/The last check failed: upstream service unavailable/),
    ).toBeInTheDocument()
    expect(screen.getByText(/Polymarket takes orders from this server: IE/)).toBeInTheDocument()
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
    renderApp('/admin/config/history')

    const rows = within(await screen.findByRole('table')).getAllByRole('row')
    expect(rows[1]).toHaveTextContent('Bot token')
    expect(rows[1]).toHaveTextContent('Replaced or cleared')
    expect(rows[1]).toHaveTextContent('root@example.com')
    expect(rows[2]).toHaveTextContent('Trusted proxies')
    expect(rows[2]).toHaveTextContent('none → 10.0.0.0/8, fd00::/8')
    expect(rows[2]).toHaveTextContent('A deleted account')
  })

  it('shows each page on the overview, with what is set there (D391)', async () => {
    config()
    renderApp('/admin/config')

    const pages = await screen.findByRole('heading', { name: 'Pages' })
    const cards = within(pages.closest('section')!)
    const card = cards.getByRole('link', { name: /^Accounts/ })
    expect(card).toHaveAttribute('href', '/admin/config/accounts')
    expect(card).toHaveTextContent('3 settings · 1 saved · 2 set by the environment or config file')
    expect(cards.getByRole('link', { name: /^Logging/ })).toHaveTextContent('1 setting')
    expect(cards.getByRole('link', { name: /^History/ })).toHaveAttribute(
      'href',
      '/admin/config/history',
    )
  })

  it('finds a setting across the pages, and opens its page at it (D391)', async () => {
    config()
    const { user, router } = renderApp('/admin/config')

    await screen.findByRole('heading', { name: 'Polymarket access' })
    await user.keyboard('/')
    const search = screen.getByRole('searchbox', { name: 'Search settings' })
    expect(search).toHaveFocus()

    // By label, key or description, every word.
    await user.type(search, 'proxies')
    const found = within(screen.getByRole('navigation', { name: 'Matching settings' }))
    expect(found.getByText('Network')).toBeInTheDocument()
    expect(found.getByRole('link', { name: 'Trusted proxies' })).toHaveAttribute(
      'href',
      '/admin/config/network#http.trusted_proxies',
    )

    await user.keyboard('{Enter}')
    expect(router.state.location.pathname).toBe('/admin/config/network')
    expect(router.state.location.hash).toBe('#http.trusted_proxies')
    await waitFor(() => expect(screen.getByLabelText('Trusted proxies')).toHaveFocus())

    await user.clear(search)
    await user.type(search, 'session idle')
    await user.click(
      within(screen.getByRole('navigation', { name: 'Matching settings' })).getByRole('link', {
        name: 'Idle sign-out',
      }),
    )
    await waitFor(() => expect(screen.getByLabelText('Idle sign-out')).toHaveFocus())

    await user.clear(search)
    await user.type(search, 'nothing like this')
    expect(screen.getByText('No settings match “nothing like this”.')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(screen.getByRole('navigation', { name: 'Config pages' })).toBeInTheDocument()
  })

  it('picks a page from a dropdown on narrow screens', async () => {
    config()
    const { user, router } = renderApp('/admin/config')

    const picker = await screen.findByRole('combobox', { name: 'Page' })
    expect(picker).toHaveValue('')
    await user.selectOptions(picker, 'Telegram')

    expect(router.state.location.pathname).toBe('/admin/config/telegram')
    expect(await screen.findByLabelText('Bot token')).toBeInTheDocument()
  })

  it('opens the overview for a page it does not have', async () => {
    config()
    const { router } = renderApp('/admin/config/nope')

    expect(await screen.findByRole('heading', { name: 'Polymarket access' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/admin/config')
  })
})
