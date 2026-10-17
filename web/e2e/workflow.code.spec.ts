import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

type Saved = {
  version: number
  nodes: { id: string; type: string; params?: Record<string, unknown>; position: { x: number } }[]
  connections: { from: string; to: string }[]
}

test('a workflow is edited as code: Apply puts it on the canvas, Save keeps it', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('as code'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'log', type: 'output.log', params: { message: 'hello' } },
  ])
  await page.goto(`/workflows/${id}`)
  await expect(canvasNode(page, 'log')).toBeVisible()

  await page.getByRole('tab', { name: 'Code' }).click()
  const editor = page.locator('.monaco-editor')
  await expect(editor.locator('.view-lines')).toContainText('w.node("log", "output.log"')
  await expect(page.getByText('The canvas shows this code.')).toBeVisible()

  // A node more, before the closing line; the type error in it does not stop Apply (D65).
  await editor.click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.press('ArrowUp')
  await page.keyboard.press('Home')
  await page.keyboard.insertText(
    'const more = w.node("more", "output.log", { message: "more" })\nlog.to(more)\nconst n: number = "one"\n',
  )
  await expect(page.getByText(/^1 type error: Apply still works/)).toBeVisible({ timeout: 20_000 })
  await expect(page.getByText(/^Not applied yet/)).toBeVisible()

  await page.getByRole('button', { name: 'Apply', exact: true }).click()
  await expect(page.getByText('The canvas shows this code.')).toBeVisible()

  await page.getByRole('tab', { name: 'Canvas' }).click()
  await expect(canvasNode(page, 'more')).toBeVisible()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()

  const saved = (await (await page.request.get(`/api/v1/workflows/${id}`)).json()) as Saved
  expect(saved.nodes.map((n) => [n.id, n.position.x])).toEqual([
    ['manual', 0],
    ['log', 300],
    ['more', 600], // new: after the node feeding it
  ])
  expect(saved.connections.map((c) => `${c.from}->${c.to}`)).toEqual(['manual->log', 'log->more'])
})

test('code that does not compile or run is refused where it is wrong, the canvas as it was', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('refused'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'log', type: 'output.log' },
  ])
  await page.goto(`/workflows/${id}`)
  await expect(canvasNode(page, 'log')).toBeVisible()

  // Playwright runs macOS shortcuts like Cmd+A and Cmd+Z on Monaco's hidden text area, not
  // as Monaco's own: each case starts from the code printed afresh instead.
  const editor = page.locator('.monaco-editor')
  async function tryCode(where: 'end' | 'before the last line', text: string) {
    await page.getByRole('tab', { name: 'Code' }).click()
    await expect(editor.locator('.view-lines')).toContainText('export default workflow(')
    await editor.click()
    await page.keyboard.press('ControlOrMeta+End')
    if (where !== 'end') {
      await page.keyboard.press('ArrowUp')
      await page.keyboard.press('Home')
    }
    await page.keyboard.insertText(text)
    await page.getByRole('button', { name: 'Apply', exact: true }).click()
  }

  // Leaving the code asks; discarding it leaves the canvas as it was.
  async function discard() {
    await page.getByRole('tab', { name: 'Canvas' }).click()
    const dialog = page.getByRole('alertdialog')
    await expect(dialog).toContainText('Apply your code?')
    await dialog.getByRole('button', { name: 'Discard' }).click()
    await expect(canvasNode(page, 'log')).toBeVisible()
    await expect(page.getByText(/Unsaved changes/)).toHaveCount(0)
  }

  // Code that does not compile: where, by line and column. Nothing Monaco closes by itself,
  // like a bracket.
  await tryCode('end', 'const = 1\n')
  await expect(
    page.getByText(/^Not applied: line \d+, column \d+: Expected identifier but found "="/),
  ).toBeVisible()
  await expect(page.getByText(/^\d+:\d+ Expected identifier but found "="/)).toBeVisible()
  await discard()

  // A node declared twice: the builder throws when it runs.
  await tryCode('before the last line', 'w.node("log", "output.log")\n')
  await expect(page.getByText('Not applied: Error: node "log" is declared twice')).toBeVisible()
  await discard()
})

test('a Filter is written in its Code setting, and runs as written', async ({ page }) => {
  const id = await createWorkflow(page, unique('filter'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'keep', type: 'code.filter' },
  ])
  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'keep').click()

  const settings = page.getByRole('complementary', { name: 'Settings' })
  const editor = settings.locator('.monaco-editor')
  await expect(editor.locator('.view-lines')).toContainText('export function keep')

  // The starter keeps items with a question, which the manual trigger's has not: written to
  // keep those without one, it keeps it. Three clicks select a line in Monaco.
  const lines = editor.locator('.view-lines')
  await expect(lines).toContainText('item.question !== undefined')
  await lines.getByText('return', { exact: true }).click({ clickCount: 3 })
  await page.keyboard.insertText('  return item.question === undefined\n')
  await expect(lines).toContainText('item.question === undefined')
  await expect(lines).not.toContainText('!==')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()

  const res = await page.request.post(`/api/v1/workflows/${id}/run`)
  expect(res.status()).toBe(200)

  const run = (await res.json()) as {
    status: string
    runs: { nodeId: string; output?: Record<string, number> }[]
  }
  expect(run.status).toBe('succeeded')
  expect(run.runs.find((r) => r.nodeId === 'keep')?.output).toEqual({ main: 1 })
})
