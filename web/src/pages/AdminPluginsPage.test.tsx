import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, apiError, registration, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const root: Schemas['User'] = { ...alice, id: 'u-0', email: 'root@example.com', role: 'admin' }
const bob: Schemas['User'] = { ...alice, id: 'u-2', email: 'bob@example.com' }

const manifest = (version: string, permissions: string[]) => ({
  id: 'weather',
  version,
  name: 'Weather',
  description: 'Forecasts for markets about the weather.',
  permissions,
  nodes: [{ name: 'forecast', label: 'Forecast' }],
  credentialTypes: [{ name: 'key', label: 'Weather key' }],
})

function version(
  v: string,
  permissions: string[],
  overrides: Partial<Schemas['PluginVersion']> = {},
): Schemas['PluginVersion'] {
  return {
    version: v,
    permissions,
    manifest: manifest(v, permissions),
    signer: '',
    keyId: '',
    installedBy: 'u-0',
    installedAt: '2026-10-22T12:00:00Z',
    approved: false,
    ...overrides,
  }
}

const running: Schemas['Plugin'] = {
  id: 'weather',
  name: 'Weather',
  version: '1.0.0',
  enabled: true,
  everyone: true,
  users: [],
  caps: { dataCap: 0, memoryCap: 0 },
  versions: [
    version('1.1.0', ['fetch:api.weather.example', 'orders']),
    version('1.0.0', ['fetch:api.weather.example'], {
      approved: true,
      approvedAt: '2026-10-22T12:01:00Z',
      signer: 'Alice',
    }),
  ],
  status: {
    version: '1.0.0',
    state: 'running',
    since: '2026-10-22T12:02:00Z',
    crashes: 1,
    lastError: 'exit status 3',
    log: ['starting', 'listening'],
    sandboxed: true,
    memory: 40 << 20,
    memoryCap: 256 << 20,
    data: 1 << 20,
    dataCap: 1 << 30,
  },
  restart: false,
}

const sandbox: Schemas['PluginSandbox'] = { available: true, why: '', unsandboxed: false }

function plugins(
  list: Schemas['Plugin'][],
  found: Schemas['FoundPlugin'][] = [],
  sb: Schemas['PluginSandbox'] = sandbox,
) {
  server.use(
    http.get('/api/v1/me', () => HttpResponse.json(root)),
    registration('invite'),
    http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    http.get('/api/v1/admin/plugins', () =>
      HttpResponse.json({ plugins: list, found, sandbox: sb }),
    ),
    http.get('/api/v1/admin/plugins/left-behind', () => HttpResponse.json([])),
  )
}

const noSandbox = 'nothing can be sandboxed here: only Linux has the sandbox, and this is darwin'

describe('admin plugins page', () => {
  it('is for admins only', async () => {
    server.use(
      signedIn,
      http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    )
    renderApp('/admin/plugins')

    expect(await screen.findByText('This page is for admins.')).toBeInTheDocument()
  })

  it('is a tab of the admin pages', async () => {
    plugins([])
    server.use(
      http.get('/api/v1/admin/settings', () =>
        HttpResponse.json({ settings: [], restartPending: false }),
      ),
      http.get('/api/v1/admin/settings/history', () => HttpResponse.json({ changes: [] })),
      http.get('/api/v1/admin/geoblock', () =>
        HttpResponse.json({ checked: false, blocked: false }),
      ),
    )
    const { user, router } = renderApp('/admin/config')

    const tabs = within(await screen.findByRole('navigation', { name: 'Admin pages' }))
    await user.click(tabs.getByRole('link', { name: 'Plugins' }))

    expect(router.state.location.pathname).toBe('/admin/plugins')
    expect(await screen.findByText('No plugin is installed yet.')).toBeInTheDocument()
  })

  it('shows each plugin: how it runs, who signed it, what waits for approval', async () => {
    plugins([running], [{ id: 'copied', version: '2.0.0' }])
    renderApp('/admin/plugins')

    const card = within(await screen.findByRole('article', { name: 'Weather' }))
    expect(card.getByText('Running')).toBeInTheDocument()
    expect(card.getByText('Version 1.0.0. Signed by Alice.')).toBeInTheDocument()
    expect(card.getByText(/1 crash in the last 10 minutes/)).toBeInTheDocument()
    expect(card.getByText('exit status 3')).toBeInTheDocument()
    expect(card.getByText(/Version 1.1.0 waits for your approval. Not signed./)).toBeInTheDocument()
    expect(card.getByText('Everyone may use it')).toBeInTheDocument()

    expect(
      screen.getByRole('heading', { name: 'Found in the plugins directory' }),
    ).toBeInTheDocument()
    expect(screen.getByText('copied 2.0.0')).toBeInTheDocument()
  })

  it('installs an archive, then asks for each permission to be ticked (D331)', async () => {
    let list: Schemas['Plugin'][] = []
    let uploaded: { type: string | null; size: number } | undefined
    let approved: unknown

    plugins([])
    server.use(
      http.get('/api/v1/admin/plugins', () =>
        HttpResponse.json({ plugins: list, found: [], sandbox }),
      ),
      http.post('/api/v1/admin/plugins', async ({ request }) => {
        uploaded = {
          type: request.headers.get('Content-Type'),
          size: (await request.arrayBuffer()).byteLength,
        }
        list = [
          {
            ...running,
            version: '',
            versions: [version('1.0.0', ['fetch:*', 'telegram'])],
            status: undefined,
          },
        ]

        return HttpResponse.json(
          {
            id: 'weather',
            version: '1.0.0',
            name: 'Weather',
            permissions: ['fetch:*', 'telegram'],
            newPermissions: ['fetch:*', 'telegram'],
            upgrade: false,
            signed: false,
            signer: '',
            keyId: '',
            warning: 'It is not signed.',
          } satisfies Schemas['InstalledPlugin'],
          { status: 201 },
        )
      }),
      http.post('/api/v1/admin/plugins/weather/versions/1.0.0/approve', async ({ request }) => {
        approved = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/admin/plugins')

    const archive = new File([new Uint8Array([0x1f, 0x8b, 1, 2, 3])], 'weather-1.0.0.tar.gz', {
      type: 'application/gzip',
    })
    await user.upload(await screen.findByLabelText('Archive'), archive)
    await user.click(screen.getByRole('button', { name: 'Install' }))

    expect(uploaded).toEqual({ type: 'application/gzip', size: 5 })

    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('Approve Weather 1.0.0')).toBeInTheDocument()
    expect(dialog.getByText('Not signed')).toBeInTheDocument()
    expect(dialog.getByText('Nodes: Forecast')).toBeInTheDocument()
    expect(dialog.getByText('Credential types: Weather key')).toBeInTheDocument()
    expect(dialog.getByText('Take care')).toBeInTheDocument() // fetch:*, not telegram

    const approve = dialog.getByRole('button', { name: 'Approve' })
    expect(approve).toBeDisabled()

    await user.click(dialog.getByRole('checkbox', { name: 'Reach any address on the internet' }))
    await user.click(
      dialog.getByRole('checkbox', { name: 'Message the people who use it on Telegram' }),
    )
    expect(approve).toBeDisabled() // the warning of an archive nobody signed, still

    await user.click(dialog.getByRole('checkbox', { name: 'I know where it came from' }))
    await user.click(approve)

    await waitFor(() => expect(approved).toEqual({ permissions: ['fetch:*', 'telegram'] }))
    expect(
      await screen.findByText('Weather 1.0.0 is approved: it runs once the server restarts'),
    ).toBeInTheDocument()
  })

  it('marks what an upgrade asks for that is new', async () => {
    plugins([running])
    const { user } = renderApp('/admin/plugins')

    const card = within(await screen.findByRole('article', { name: 'Weather' }))
    await user.click(card.getByRole('button', { name: 'Review' }))

    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText(/replacing 1.0.0/)).toBeInTheDocument()

    const orders = dialog
      .getByRole('checkbox', { name: 'Place and cancel orders' })
      .closest('label')!
    expect(within(orders).getByText('New')).toBeInTheDocument()
    expect(within(orders).getByText('Take care')).toBeInTheDocument()

    const fetch = dialog
      .getByRole('checkbox', { name: 'Reach api.weather.example' })
      .closest('label')!
    expect(within(fetch).queryByText('New')).not.toBeInTheDocument()
  })

  it('says why an approval was refused', async () => {
    plugins([running])
    server.use(
      http.post('/api/v1/admin/plugins/weather/versions/1.1.0/approve', () =>
        apiError(
          400,
          'invalid_input',
          'invalid input: its files ask for other permissions than when it was installed',
        ),
      ),
    )
    const { user } = renderApp('/admin/plugins')

    await user.click(await screen.findByRole('button', { name: 'Review' }))
    const dialog = within(await screen.findByRole('dialog'))
    await user.click(dialog.getByRole('checkbox', { name: 'Reach api.weather.example' }))
    await user.click(dialog.getByRole('checkbox', { name: 'Place and cancel orders' }))
    await user.click(dialog.getByRole('checkbox', { name: 'I know where it came from' }))
    await user.click(dialog.getByRole('button', { name: 'Approve' }))

    expect(
      await dialog.findByText(/its files ask for other permissions than when it was installed/),
    ).toBeInTheDocument()
  })

  it('warns again when the key that signed it is no longer trusted (P21)', async () => {
    let refused = false
    const signed = (signer: string): Schemas['Plugin'] => ({
      ...running,
      versions: [
        version('1.1.0', ['fetch:api.weather.example'], { signer, keyId: 'E7620F1842B4E81F' }),
        running.versions[1],
      ],
    })

    plugins([signed('Alice')])
    server.use(
      http.get('/api/v1/admin/plugins', () =>
        HttpResponse.json({ plugins: [signed(refused ? '' : 'Alice')], found: [], sandbox }),
      ),
      http.post('/api/v1/admin/plugins/weather/versions/1.1.0/approve', () => {
        refused = true
        return apiError(
          400,
          'invalid_input',
          'invalid input: Alice, which signed it, is no longer a key you trust: look at it again before you approve it',
        )
      }),
    )
    const { user } = renderApp('/admin/plugins')

    await user.click(await screen.findByRole('button', { name: 'Review' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('Signed by Alice.')).toBeInTheDocument()
    await user.click(dialog.getByRole('checkbox', { name: 'Reach api.weather.example' }))
    await user.click(dialog.getByRole('button', { name: 'Approve' }))

    expect(await dialog.findByText(/is no longer a key you trust/)).toBeInTheDocument()
    expect(
      await dialog.findByText('Signed by a key you do not trust (E7620F1842B4E81F)'),
    ).toBeInTheDocument()
    expect(dialog.getByRole('checkbox', { name: 'I know where it came from' })).toBeInTheDocument()
    expect(dialog.getByRole('button', { name: 'Approve' })).toBeDisabled()
  })

  it('chooses who may use a plugin (D282)', async () => {
    let sent: unknown

    plugins([running])
    server.use(
      http.get('/api/v1/admin/users', () => HttpResponse.json({ users: [root, alice, bob] })),
      http.put('/api/v1/admin/plugins/weather/users', async ({ request }) => {
        sent = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/admin/plugins')

    await user.click(await screen.findByRole('button', { name: 'Who may use it' }))
    const dialog = within(await screen.findByRole('dialog'))
    await user.click(dialog.getByRole('switch'))
    await user.type(dialog.getByLabelText('Find a user'), 'bob')
    await user.click(await dialog.findByRole('checkbox'))
    await user.click(dialog.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(sent).toEqual({ everyone: false, users: ['u-2'] }))
  })

  it('disables, and uninstalls, its data kept or removed too (D342, D348)', async () => {
    const calls: string[] = []

    plugins([running])
    server.use(
      http.post('/api/v1/admin/plugins/weather/disable', () => {
        calls.push('disable')
        return new HttpResponse(null, { status: 204 })
      }),
      http.delete('/api/v1/admin/plugins/weather', ({ request }) => {
        calls.push(`uninstall, data=${new URL(request.url).searchParams.get('data')}`)
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/admin/plugins')

    const card = within(await screen.findByRole('article', { name: 'Weather' }))
    await user.click(card.getByRole('switch', { name: 'Enabled' }))
    await waitFor(() => expect(calls).toEqual(['disable']))

    await user.click(card.getByRole('button', { name: 'Uninstall' }))
    let confirm = within(await screen.findByRole('alertdialog'))
    expect(confirm.getByRole('checkbox', { name: /Also remove its data/ })).not.toBeChecked()
    await user.click(confirm.getByRole('button', { name: 'Uninstall' }))
    await waitFor(() => expect(calls).toEqual(['disable', 'uninstall, data=null']))
    expect(await screen.findByText('Weather is uninstalled, its data kept')).toBeInTheDocument()

    await user.click(card.getByRole('button', { name: 'Uninstall' }))
    confirm = within(await screen.findByRole('alertdialog'))
    await user.click(confirm.getByRole('checkbox', { name: /Also remove its data/ }))
    await user.click(confirm.getByRole('button', { name: 'Uninstall' }))
    await waitFor(() =>
      expect(calls).toEqual(['disable', 'uninstall, data=null', 'uninstall, data=true']),
    )
    expect(await screen.findByText('Weather is uninstalled, its data removed')).toBeInTheDocument()
  })

  it('shows what a running plugin takes, each against its cap, sandboxed (D351)', async () => {
    plugins([running])
    renderApp('/admin/plugins')

    const card = within(await screen.findByRole('article', { name: 'Weather' }))
    expect(card.getByText('Sandboxed')).toBeInTheDocument()
    expect(card.getByRole('meter', { name: 'Memory' })).toHaveAttribute(
      'aria-valuetext',
      '40 MiB of 256 MiB',
    )
    expect(card.getByRole('meter', { name: 'Data' })).toHaveAttribute(
      'aria-valuetext',
      '1 MiB of 1 GiB',
    )
    expect(card.queryByText(/near its cap/)).not.toBeInTheDocument()
  })

  it('warns of a plugin near its cap, and says where memory is not watched', async () => {
    plugins([{ ...running, status: { ...running.status!, memory: 0, data: 1000 << 20 } }])
    renderApp('/admin/plugins')

    const card = within(await screen.findByRole('article', { name: 'Weather' }))
    expect(card.getByText('Not measured: it is watched on Linux.')).toBeInTheDocument()
    expect(card.queryByRole('meter', { name: 'Memory' })).not.toBeInTheDocument()
    expect(card.getByRole('meter', { name: 'Data' })).toHaveAttribute(
      'aria-valuetext',
      '1000 MiB of 1 GiB, near its cap',
    )
    expect(card.getByText(/near its cap/)).toBeInTheDocument()
  })

  it('says plugins do not start where there is no sandbox (D338)', async () => {
    plugins([], [], { available: false, why: noSandbox, unsandboxed: false })
    renderApp('/admin/plugins')

    expect(await screen.findByText('Plugins do not start here')).toBeInTheDocument()
    expect(
      screen.getByText(/^Nothing can be sandboxed here: only Linux has the sandbox/),
    ).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Config page' })).toHaveAttribute(
      'href',
      '/admin/config',
    )
  })

  it('warns while plugins run unsandboxed (D338)', async () => {
    plugins([{ ...running, status: { ...running.status!, sandboxed: false } }], [], {
      available: false,
      why: noSandbox,
      unsandboxed: true,
    })
    renderApp('/admin/plugins')

    expect(await screen.findByText('Plugins run unsandboxed here')).toBeInTheDocument()
    const card = within(screen.getByRole('article', { name: 'Weather' }))
    expect(card.getByText('Not sandboxed')).toBeInTheDocument()
  })

  it("sets a plugin's own caps, each blank for the Config page's (D346, D350)", async () => {
    let sent: unknown

    plugins([{ ...running, caps: { dataCap: 2048, memoryCap: 0 } }])
    server.use(
      http.get('/api/v1/admin/settings', () =>
        HttpResponse.json({
          restartPending: false,
          settings: [
            { key: 'plugins.data_cap', value: 1024 },
            { key: 'plugins.memory_cap', value: 256 },
          ],
        }),
      ),
      http.put('/api/v1/admin/plugins/weather/caps', async ({ request }) => {
        sent = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/admin/plugins')

    await user.click(await screen.findByRole('button', { name: 'Caps' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(await dialog.findByPlaceholderText("The Config page's: 256")).toBeInTheDocument()
    expect(dialog.getByLabelText('Data (MiB)')).toHaveValue(2048)

    await user.clear(dialog.getByLabelText('Data (MiB)'))
    await user.type(dialog.getByLabelText('Memory (MiB)'), '512')
    await user.click(dialog.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(sent).toEqual({ dataCap: 0, memoryCap: 512 }))
    expect(
      await screen.findByText("Weather's caps saved: they apply as it is next measured"),
    ).toBeInTheDocument()
  })

  it('names a cap out of bounds', async () => {
    plugins([running])
    server.use(
      http.get('/api/v1/admin/settings', () =>
        HttpResponse.json({ restartPending: false, settings: [] }),
      ),
      http.put('/api/v1/admin/plugins/weather/caps', () =>
        HttpResponse.json(
          {
            error: {
              code: 'invalid_input',
              message: 'invalid input',
              problems: [{ param: 'memoryCap', message: 'A memory cap is 16 to 65536 MiB.' }],
            },
          },
          { status: 400 },
        ),
      ),
    )
    const { user } = renderApp('/admin/plugins')

    await user.click(await screen.findByRole('button', { name: 'Caps' }))
    const dialog = within(await screen.findByRole('dialog'))
    await user.type(dialog.getByLabelText('Memory (MiB)'), '8')
    await user.click(dialog.getByRole('button', { name: 'Save' }))

    expect(await dialog.findByText('A memory cap is 16 to 65536 MiB.')).toBeInTheDocument()
  })

  it('lists what plugins left behind, and removes it (D342)', async () => {
    let removed = false

    plugins([])
    server.use(
      http.get('/api/v1/admin/plugins/left-behind', () =>
        HttpResponse.json(removed ? [] : [{ id: 'news', files: 3 << 20, stored: 512 }]),
      ),
      http.delete('/api/v1/admin/plugins/left-behind/news', () => {
        removed = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/admin/plugins')

    expect(await screen.findByRole('heading', { name: 'Data left behind' })).toBeInTheDocument()
    expect(
      screen.getByText('3 MiB in its directory, 512 B stored by its nodes'),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Remove' }))
    const confirm = within(await screen.findByRole('alertdialog'))
    expect(confirm.getByText('Remove what news left behind?')).toBeInTheDocument()
    await user.click(confirm.getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(removed).toBe(true))
    expect(await screen.findByText('What news left behind is removed')).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByRole('heading', { name: 'Data left behind' })).not.toBeInTheDocument(),
    )
  })

  it('starts a crashed plugin again (D320)', async () => {
    let started = false

    plugins([{ ...running, status: { ...running.status!, state: 'crashed', crashes: 5 } }])
    server.use(
      http.post('/api/v1/admin/plugins/weather/start', () => {
        started = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/admin/plugins')

    expect(await screen.findByText('Crashed')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Start again' }))
    await waitFor(() => expect(started).toBe(true))
  })

  it("sets a plugin's own settings, a secret write-only (D329)", async () => {
    let sent: unknown
    const declared: Schemas['PluginSetting'][] = [
      {
        key: 'units',
        label: 'Units',
        description: 'How warm.',
        kind: 'enum',
        options: ['celsius', 'kelvin'],
        secret: false,
        isSet: false,
        value: 'celsius',
        default: 'celsius',
      },
      {
        key: 'api_key',
        label: 'API key',
        description: '',
        kind: 'string',
        secret: true,
        isSet: true,
        value: null,
        default: null,
      },
      {
        key: 'days',
        label: 'Days ahead',
        description: '',
        kind: 'int',
        secret: false,
        isSet: false,
        value: 3,
        default: 3,
      },
    ]

    const withSettings = {
      ...running,
      versions: running.versions.map((v) =>
        v.approved ? { ...v, manifest: { ...v.manifest, settings: declared } } : v,
      ),
    }

    plugins([withSettings])
    server.use(
      http.get('/api/v1/admin/plugins/weather/settings', () =>
        HttpResponse.json({ settings: declared }),
      ),
      http.patch('/api/v1/admin/plugins/weather/settings', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({ settings: declared })
      }),
    )
    const { user } = renderApp('/admin/plugins')

    const card = within(await screen.findByRole('article', { name: 'Weather' }))
    await user.click(card.getByRole('button', { name: 'Settings' }))

    const dialog = within(await screen.findByRole('dialog'))
    expect(await dialog.findByLabelText('Units')).toHaveValue('celsius')
    expect(dialog.getByLabelText('API key')).toHaveValue('')
    expect(dialog.getByLabelText('API key')).toHaveAttribute(
      'placeholder',
      'Saved. Type a new value to replace it.',
    )

    await user.selectOptions(dialog.getByLabelText('Units'), 'kelvin')
    await user.type(dialog.getByLabelText('API key'), 'k-123')
    await user.click(dialog.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(sent).toEqual({ values: { units: 'kelvin', api_key: 'k-123' } })) // only what changed
  })

  it("says which of a plugin's settings is wrong", async () => {
    const days: Schemas['PluginSetting'] = {
      key: 'days',
      label: 'Days ahead',
      description: '',
      kind: 'int',
      secret: false,
      isSet: false,
      value: 3,
      default: 3,
    }

    plugins([
      {
        ...running,
        versions: running.versions.map((v) =>
          v.approved ? { ...v, manifest: { ...v.manifest, settings: [days] } } : v,
        ),
      },
    ])
    server.use(
      http.get('/api/v1/admin/plugins/weather/settings', () =>
        HttpResponse.json({ settings: [days] }),
      ),
      http.patch('/api/v1/admin/plugins/weather/settings', () =>
        HttpResponse.json(
          {
            error: {
              code: 'invalid_input',
              message: 'invalid input',
              problems: [{ param: 'days', message: 'Days ahead: must be between 1 and 14' }],
            },
          },
          { status: 400 },
        ),
      ),
    )
    const { user } = renderApp('/admin/plugins')

    await user.click(await screen.findByRole('button', { name: 'Settings' }))
    const dialog = within(await screen.findByRole('dialog'))
    await user.clear(await dialog.findByLabelText('Days ahead'))
    await user.type(dialog.getByLabelText('Days ahead'), '40')
    await user.click(dialog.getByRole('button', { name: 'Save' }))

    expect(await dialog.findByText('Days ahead: must be between 1 and 14')).toBeInTheDocument()
  })

  it('restarts the server and waits until plugins run as decided (D317)', async () => {
    let restarted = false

    plugins([])
    server.use(
      http.get('/api/v1/admin/plugins', () =>
        HttpResponse.json({ plugins: [{ ...running, restart: !restarted }], found: [], sandbox }),
      ),
      http.post('/api/v1/admin/restart', () => {
        restarted = true
        return new HttpResponse(null, { status: 202 })
      }),
    )
    const { user } = renderApp('/admin/plugins')

    expect(await screen.findByText('A restart is needed')).toBeInTheDocument()
    expect(screen.getByText('Once the server restarts: Weather 1.0.0 runs.')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Restart now' }))
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Restart' }),
    )

    expect(await screen.findByText('Server restarted', {}, { timeout: 3000 })).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('A restart is needed')).not.toBeInTheDocument())
  })
})
