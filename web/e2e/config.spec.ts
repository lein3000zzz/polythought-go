import { expect, test } from './fixtures'

// While the server restarts, requests fail for a moment: the proxy answers 502 and the
// live event stream drops until it reconnects.
test.use({ expectedConsoleError: /status of 50\d|net::ERR_/ })

test('an admin changes settings live and restarts the server for the rest', async ({ page }) => {
  await page.goto('/admin/config')

  // `make e2e` sets registration in the environment, which locks it here.
  await expect(page.getByText('Set by POLYTHOUGHT_REGISTRATION')).toBeVisible()
  await expect(page.getByLabel('Registration')).toBeDisabled()

  // A live setting applies at once: the password form reads the new rule.
  await page.getByLabel('Shortest password').fill('20')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()

  await page.goto('/settings')
  await expect(page.getByText('At least 20 characters.')).toBeVisible()

  // Back to the default, for the tests that create accounts.
  await page.goto('/admin/config')
  await page.getByRole('button', { name: 'Reset to default' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  await expect(page.getByLabel('Shortest password')).toHaveValue('')

  // One that applies on restart waits for it.
  await page.getByLabel('Log format').selectOption('json')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('A restart is needed')).toBeVisible()

  await page.getByRole('button', { name: 'Restart now' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Restart' }).click()
  await expect(page.getByText('Server restarted')).toBeVisible({ timeout: 30_000 })
  await expect(page.getByText('A restart is needed')).toBeHidden()

  // Still signed in, and the history has every change.
  await page.reload()
  await expect(page.getByLabel('Log format')).toHaveValue('json')
  const history = page.getByRole('table')
  await expect(history.getByRole('row', { name: /Log format/ })).toContainText('text → json')
  await expect(history.getByRole('row', { name: /Shortest password/ })).toHaveCount(2)
})
