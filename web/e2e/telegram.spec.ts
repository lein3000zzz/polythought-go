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
