import { createWorkflow, expect, test, unique } from './fixtures'

// The host's test plugin, packed for the containers by make e2e (tools/pluginpack).
const archive = 'e2e/.plugins/echo.tar.gz'

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

    // What admins did with it, kept.
    await card.getByRole('button', { name: 'History' }).click()
    const history = page.getByRole('dialog')
    await expect(history.getByText(/^approved 1\.2\.0/)).toBeVisible()
    await expect(history.getByText(/^installed 1\.2\.0: not signed/)).toBeVisible()
  })
})
