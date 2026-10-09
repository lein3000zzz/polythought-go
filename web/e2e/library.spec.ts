import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

test('a scenario explains itself and becomes your own workflow', async ({ page }) => {
  await page.goto('/library')
  await page.getByRole('link', { name: /Your first workflow/ }).click()

  await expect(page.getByText('A workflow is a chain of nodes.', { exact: false })).toBeVisible()
  await canvasNode(page, 'search').click()
  await expect(
    page.getByRole('complementary', { name: 'About' }).getByText(/Finds open markets with bitcoin/),
  ).toBeVisible()

  const name = unique('first')
  await page.getByRole('button', { name: 'Use this' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Name').fill(name)
  await dialog.getByRole('button', { name: 'Create workflow' }).click()

  await expect(page).toHaveURL(/\/workflows\/[0-9a-f-]{36}$/)
  await expect(page.getByLabel('Workflow name')).toHaveValue(name)
  await expect(
    canvasNode(page, 'top').getByText(/Sorts by it and keeps the top five/),
  ).toBeVisible()
  await expect(page.getByText('Inactive')).toBeVisible()
})

test('a shared workflow keeps wallets and webhook URLs private', async ({ page }) => {
  const name = unique('shared')
  const id = await createWorkflow(page, name, [
    { id: 'manual', type: 'trigger.manual' },
    {
      id: 'chat',
      type: 'output.webhook',
      params: { url: 'https://discord.com/api/webhooks/1/secret', message: 'hi' },
    },
  ])

  await page.goto(`/workflows/${id}`)
  await page.getByRole('button', { name: 'More actions' }).click()
  await page.getByRole('menuitem', { name: /Share to library/ }).click()
  const share = page.getByRole('dialog')
  await share.getByLabel('Summary').fill('Says hi')
  await share.getByRole('button', { name: 'Share' }).click()
  await expect(page.getByText('Shared to the library')).toBeVisible()

  await page.goto('/library')
  const card = page.getByRole('link', { name: new RegExp(name) })
  await expect(card.getByText('Yours')).toBeVisible()
  await expect(card.getByText('Asks for a webhook URL')).toBeVisible()

  await card.click()
  await expect(page.getByText('discord.com/api/webhooks')).toHaveCount(0)
  await page.getByRole('button', { name: 'Use this' }).click()
  await expect(page.getByRole('dialog').getByLabel(/Webhook URL/)).toHaveValue('')
})
