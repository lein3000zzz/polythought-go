import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

// The system clipboard, as the editor's copy and paste use it.
test.use({ permissions: ['clipboard-read', 'clipboard-write'] })

test('nodes are copied without their personal settings, pasted, and undone (D420)', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('shortcuts'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'hook', type: 'output.webhook', params: { url: 'https://hooks.example/secret-path' } },
  ])

  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'hook').click()
  await page.keyboard.press('ControlOrMeta+c')

  const copied = JSON.parse(await page.evaluate(() => navigator.clipboard.readText())) as {
    nodes: { id: string; params?: Record<string, unknown> }[]
  }
  expect(copied.nodes.map((n) => n.id)).toEqual(['hook'])
  expect(copied.nodes[0].params?.url, 'a webhook URL stays behind').toBeUndefined()

  await page.keyboard.press('ControlOrMeta+v')
  await expect(canvasNode(page, 'hook2')).toBeVisible()

  await page.keyboard.press('ControlOrMeta+z')
  await expect(canvasNode(page, 'hook2')).toHaveCount(0)
  await page.keyboard.press('ControlOrMeta+Shift+z')
  await expect(canvasNode(page, 'hook2')).toBeVisible()
})

test('a shortcut changed in Settings is kept for the account', async ({ page }) => {
  try {
    await page.goto('/settings/shortcuts')
    const undo = page.getByRole('listitem').filter({ hasText: 'Undo' }).first()
    await undo.getByRole('button', { name: 'Change' }).click()
    await page.keyboard.press('ControlOrMeta+u')
    await expect(page.getByText('Undo: changed')).toBeVisible()

    await page.reload()
    await expect(undo.locator('kbd')).toHaveText(/U$/)
  } finally {
    await page.request.put('/api/v1/me/shortcuts', { data: { shortcuts: {} } })
  }
})
