import { expect, test } from './fixtures'

// The test stack never asks GitHub (D424): its check is off, as the Config page says.
test('the Config page shows the version, and where the update check is', async ({ page }) => {
  await page.goto('/admin/config')
  await expect(page.getByRole('heading', { name: 'Version' })).toBeVisible()
  await expect(page.getByText('A development build')).toBeVisible()
  await expect(page.getByText(/^The update check is off/)).toBeVisible()

  await page.getByRole('link', { name: 'Check for updates' }).click()
  await expect(page).toHaveURL(/\/admin\/config\/updates#updates\.check$/)
  await expect(page.getByLabel('Check for updates')).toBeDisabled() // the environment sets it
  await expect(page.getByLabel('Updater address')).toHaveAttribute(
    'placeholder',
    'Default: http://updater:8080',
  )
})
