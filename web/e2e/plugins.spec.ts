import { createWorkflow, expect, test, unique } from './fixtures'

// The host's test plugin and the example plugin, packed for the containers by make e2e
// (tools/pluginpack).
const archive = 'e2e/.plugins/echo.tar.gz'
const example = 'e2e/.plugins/movers.tar.gz'

test.describe('a plugin', () => {
  // The server is down for a moment while it restarts.
  test.use({ expectedConsoleError: /status of 50\d|net::ERR_/ })

  test('is installed from its archive, approved permission by permission, and runs after a restart (5c)', async ({
    page,
  }) => {
    await page.goto('/admin/plugins')
    await page.getByLabel('Archive').setInputFiles(archive)
    await page.getByRole('button', { name: 'Install', exact: true }).click()

    // The permissions screen: each ticked, and the warning of an archive nobody signed (D331).
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('Approve Echo 1.2.0')).toBeVisible()
    await expect(dialog.getByText('Not signed')).toBeVisible()
    await expect(dialog.getByText('Reach 127.0.0.1')).toBeVisible()
    await expect(dialog.getByText('Place and cancel orders')).toBeVisible()

    const approve = dialog.getByRole('button', { name: 'Approve' })
    for (const box of await dialog.getByRole('checkbox').all()) {
      await expect(approve).toBeDisabled()
      await box.check()
    }

    await approve.click()
    await expect(
      page.getByText('Echo 1.2.0 is approved: it runs once the server restarts'),
    ).toBeVisible()

    // It runs from the next start (D317).
    const card = page.getByRole('article', { name: 'Echo' })
    await expect(card.getByText('Waits for a restart')).toBeVisible()
    await expect(page.getByText('Once the server restarts: Echo 1.2.0 runs.')).toBeVisible()

    await page.getByRole('button', { name: 'Restart now' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Restart' }).click()
    await expect(page.getByText('Server restarted')).toBeVisible({ timeout: 60_000 })

    await expect(card.getByText('Running')).toBeVisible()
    await expect(card.getByText('Version 1.2.0. Not signed.')).toBeVisible()

    // Under the sandbox, in the server's container (D337), what it takes against its caps
    // (D351): its memory, once measured, and its data.
    await expect(card.getByText('Sandboxed', { exact: true })).toBeVisible()
    await expect(card.getByRole('meter', { name: 'Data' })).toHaveAttribute(
      'aria-valuetext',
      / of 1 GiB$/,
    )
    await expect(async () => {
      await page.reload()
      await expect(card.getByRole('meter', { name: 'Memory' })).toHaveAttribute(
        'aria-valuetext',
        / of 256 MiB$/,
        { timeout: 1_000 },
      )
    }).toPass()

    // Its nodes are the engine's now: a workflow runs one, in the plugin's process.
    const types = (await (await page.request.get('/api/v1/node-types')).json()) as {
      nodeTypes: { name: string }[]
    }
    expect(types.nodeTypes.map((t) => t.name)).toContain('plugin.echo.count')

    const id = await createWorkflow(page, unique('plugin'), [
      { id: 'manual', type: 'trigger.manual' },
      { id: 'count', type: 'plugin.echo.count' },
    ])

    const ran = await page.request.post(`/api/v1/workflows/${id}/run`)
    expect(ran.status(), await ran.text()).toBe(200)
    expect(((await ran.json()) as { status: string }).status).toBe('succeeded')

    // Its own settings, set on its card, reach its next run (D329).
    await card.getByRole('button', { name: 'Settings' }).click()
    const settings = page.getByRole('dialog')
    await expect(settings.getByLabel('Greeting')).toHaveValue('hello')
    await settings.getByLabel('Greeting').fill('hi there')
    await settings.getByLabel('Token').fill('t-secret')
    await settings.getByRole('button', { name: 'Save' }).click()
    await expect(
      page.getByText("Echo's settings saved: its runs get them from the next"),
    ).toBeVisible()

    const greets = await createWorkflow(page, unique('greet'), [
      { id: 'manual', type: 'trigger.manual' },
      { id: 'greet', type: 'plugin.echo.greet' },
    ])

    const greeted = await page.request.post(`/api/v1/workflows/${greets}/run`)
    expect(greeted.status(), await greeted.text()).toBe(200)

    const run = (await greeted.json()) as {
      runs: { nodeId: string; samples?: Record<string, { json?: Record<string, unknown> }[]> }[]
    }
    expect(run.runs.find((r) => r.nodeId === 'greet')?.samples?.main?.[0]?.json).toEqual({
      greeting: 'hi there',
      token: 't-secret',
    })

    // Its own caps, set on its card, hold it as it is next measured (D346, D350).
    await card.getByRole('button', { name: 'Caps' }).click()
    const caps = page.getByRole('dialog')
    await expect(caps.getByLabel('Memory (MiB)')).toHaveAttribute(
      'placeholder',
      "The Config page's: 256",
    )
    await caps.getByLabel('Memory (MiB)').fill('512')
    await caps.getByRole('button', { name: 'Save' }).click()
    await expect(
      page.getByText("Echo's caps saved: they apply as it is next measured"),
    ).toBeVisible()
    await expect(async () => {
      await page.reload()
      await expect(card.getByRole('meter', { name: 'Memory' })).toHaveAttribute(
        'aria-valuetext',
        / of 512 MiB$/,
        { timeout: 1_000 },
      )
    }).toPass()

    // What admins did with it, kept.
    await card.getByRole('button', { name: 'History' }).click()
    const history = page.getByRole('dialog')
    await expect(history.getByText("caps: data: the Config page's; memory: 512 MiB")).toBeVisible()
    await expect(
      history.getByText(/^settings 1\.2\.0: greeting: "hello" → "hi there"; token changed/),
    ).toBeVisible()
    await expect(history.getByText(/^approved 1\.2\.0/)).toBeVisible()
    await expect(history.getByText(/^installed 1\.2\.0: not signed/)).toBeVisible()
    await page.keyboard.press('Escape')

    // In the editor, its nodes are in one Plugins group, each saying whose (D330).
    await page.goto('/workflows/new')
    const nodes = page.getByRole('navigation', { name: 'Node types' })
    await expect(nodes.getByRole('heading', { name: 'Plugins' })).toBeVisible()
    const trade = nodes.getByRole('button', { name: /^Trade/ })
    await expect(trade.getByText('From Echo')).toBeVisible()
    await expect(trade.getByText('Trades')).toBeVisible()

    // Uninstalled, its data kept, then what it left behind removed (D342, D348).
    await page.goto('/admin/plugins')
    await card.getByRole('button', { name: 'Uninstall' }).click()
    const uninstall = page.getByRole('alertdialog')
    await expect(
      uninstall.getByRole('checkbox', { name: /Also remove its data/ }),
    ).not.toBeChecked()
    await uninstall.getByRole('button', { name: 'Uninstall' }).click()
    await expect(page.getByText('Echo is uninstalled, its data kept')).toBeVisible()

    const left = page.getByRole('heading', { name: 'Data left behind' })
    await expect(left).toBeVisible()
    await expect(page.locator('section', { has: left }).getByText(/^echo/)).toBeVisible()
    await page.getByRole('button', { name: 'Remove' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Remove' }).click()
    await expect(page.getByText('What echo left behind is removed')).toBeVisible()
    await expect(left).toBeHidden()
  })

  test('the example plugin installs, is approved, and its node runs in a workflow (5f)', async ({
    page,
  }) => {
    await page.goto('/admin/plugins')
    await page.getByLabel('Archive').setInputFiles(example)
    await page.getByRole('button', { name: 'Install', exact: true }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('Approve Market movers 1.0.0')).toBeVisible()
    await expect(dialog.getByText('Reach api.coingecko.com')).toBeVisible()

    const approve = dialog.getByRole('button', { name: 'Approve' })
    for (const box of await dialog.getByRole('checkbox').all()) await box.check()
    await approve.click()
    await expect(
      page.getByText('Market movers 1.0.0 is approved: it runs once the server restarts'),
    ).toBeVisible()

    await page.getByRole('button', { name: 'Restart now' }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Restart' }).click()
    await expect(page.getByText('Server restarted')).toBeVisible({ timeout: 60_000 })

    const card = page.getByRole('article', { name: 'Market movers' })
    await expect(card.getByText('Running')).toBeVisible()
    await expect(card.getByText('Sandboxed', { exact: true })).toBeVisible()

    // Its node, after a market's move as its trigger would bring it: previewed, so neither
    // Telegram nor CoinGecko is needed here.
    const id = await createWorkflow(page, unique('movers'), [
      { id: 'manual', type: 'trigger.manual' },
      {
        id: 'move',
        type: 'code.transform',
        params: {
          source: `export function run() {
  return [{ question: "Will it rain?", outcome: "Yes", from: "0.40", to: "0.47" }]
}`,
        },
      },
      { id: 'alert', type: 'plugin.movers.alert', params: { send: false } },
    ])

    const res = await page.request.post(`/api/v1/workflows/${id}/run`)
    expect(res.status(), await res.text()).toBe(200)

    const run = (await res.json()) as {
      status: string
      runs: { nodeId: string; samples?: Record<string, { json?: Record<string, unknown> }[]> }[]
    }
    expect(run.status).toBe('succeeded')
    expect(run.runs.find((r) => r.nodeId === 'alert')?.samples?.main?.[0]?.json).toMatchObject({
      message: 'Will it rain? moved +7 points, from 40% to 47% (Yes).',
      sent: false,
    })

    // Its scenario is the library's.
    await page.goto('/library')
    await expect(page.getByText('Alert me when a market moves')).toBeVisible()
  })
})
