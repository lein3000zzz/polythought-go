import { canvasNode, connect, createWorkflow, expect, test, unique } from './fixtures'

test('building, saving and running a workflow', async ({ page }) => {
  const name = unique('built')

  await page.goto('/workflows')
  await page.getByRole('link', { name: 'New workflow' }).click()
  await expect(canvasNode(page, 'manual')).toBeVisible()

  // Zoom out for room, then drop a Log node to the right of the trigger.
  await page.getByRole('button', { name: 'Zoom Out' }).click()
  const pane = page.locator('.react-flow__pane')
  const box = (await pane.boundingBox())!
  await page
    .getByRole('navigation', { name: 'Node types' })
    .getByText('Log', { exact: true })
    .dragTo(pane, { targetPosition: { x: box.width * 0.8, y: box.height * 0.5 } })

  await connect(page, 'manual', 'main', 'log')
  await expect(page.locator('.react-flow__edge')).toHaveCount(1)

  const settings = page.getByRole('complementary', { name: 'Settings' })
  await canvasNode(page, 'log').click()
  await settings.getByLabel(/Message/).fill('hello from e2e')
  await page.getByLabel('Workflow name').fill(name)
  await page.keyboard.press('ControlOrMeta+s')

  await expect(page).toHaveURL(/\/workflows\/[0-9a-f-]{36}$/)
  await expect(page.getByText('Version 1')).toBeVisible()

  await page.getByRole('button', { name: 'Run', exact: true }).click()
  await expect(page.getByText(/Execution at/)).toBeVisible()
  await expect(canvasNode(page, 'log').getByLabel('Succeeded')).toBeVisible()

  await canvasNode(page, 'log').click()
  await expect(settings.getByRole('tab', { name: 'Output' })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(settings.getByText('main · 1 item')).toBeVisible()

  // The list shows it after a reload.
  await page.reload()
  await expect(page.getByLabel('Workflow name')).toHaveValue(name)
})

test('the server points at what is wrong', async ({ page }) => {
  const id = await createWorkflow(page, unique('problems'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'tight', type: 'logic.if', params: { field: 'spread', value: '0.02' } },
  ])

  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'tight').click()

  const value = page.getByRole('complementary', { name: 'Settings' }).getByLabel(/Value/)
  await value.fill('lots')
  await expect(page.getByText('Enter a number')).toBeVisible()

  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText(/Not saved: the workflow has problems/)).toBeVisible()
  await expect(canvasNode(page, 'tight').getByText('1')).toBeVisible()

  await value.fill('0.05')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()
})

test('a save based on an old version is refused', async ({ page, context }) => {
  const id = await createWorkflow(page, unique('conflict'), [
    { id: 'manual', type: 'trigger.manual' },
  ])

  await page.goto(`/workflows/${id}`)
  await expect(page.getByText('Version 1')).toBeVisible()

  const other = await context.newPage()
  await other.goto(`/workflows/${id}`)
  await other.getByLabel('Workflow name').fill('saved in another tab')
  await other.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(other.getByText('Version 2')).toBeVisible()
  await other.close()

  await page.getByLabel('Workflow name').fill('saved here')
  await page.getByRole('button', { name: 'Save', exact: true }).click()

  const dialog = page.getByRole('alertdialog')
  await expect(dialog.getByText('This workflow changed elsewhere')).toBeVisible()
  await dialog.getByRole('button', { name: 'Reload' }).click()
  await expect(page.getByLabel('Workflow name')).toHaveValue('saved in another tab')
})
