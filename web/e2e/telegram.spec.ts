import { expect, test } from './fixtures'

test('the bot follows its settings, and Settings says what it is missing', async ({ page }) => {
  await page.goto('/settings')
  await expect(page.getByText('Telegram is not set up on this instance.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Link Telegram' })).toHaveCount(0)

  // Switched on without a token, the bot cannot start, and says why. No restart needed.
  await page.goto('/admin/config')
  await page.getByLabel('Telegram bot').click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()

  await page.goto('/settings')
  await expect(page.getByText('The bot reported a problem')).toBeVisible()
  await expect(page.getByText(/there is no bot token; add one on the Config page/)).toBeVisible()

  // Off again, for the other tests.
  await page.goto('/admin/config')
  await page.getByLabel('Telegram bot').click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()

  await page.goto('/settings')
  await expect(page.getByText('Telegram is not set up on this instance.')).toBeVisible()
  await expect(page.getByText('The bot reported a problem')).toHaveCount(0)
})

test('notification choices are kept, and the editor offers the Telegram nodes', async ({
  page,
}) => {
  await page.goto('/settings')
  const failures = page.getByRole('switch', { name: 'A workflow fails' })
  await expect(failures).toBeChecked()
  await expect(page.getByText(/Link Telegram above to get them/)).toBeVisible()

  await failures.click()
  await expect(failures).not.toBeChecked()
  await page.reload()
  await expect(page.getByRole('switch', { name: 'A workflow fails' })).not.toBeChecked()

  await page.getByRole('switch', { name: 'A workflow fails' }).click()
  await expect(page.getByRole('switch', { name: 'A workflow fails' })).toBeChecked()

  await page.goto('/workflows/new')
  const list = page.getByRole('navigation', { name: 'Node types' })
  await expect(list.getByText('Telegram message', { exact: true })).toHaveCount(2) // trigger and node
  await expect(list.getByRole('heading', { name: 'Telegram' })).toBeVisible()
})
