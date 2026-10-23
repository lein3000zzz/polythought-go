import { expect, test } from './fixtures'

// While the server restarts, requests fail for a moment: the proxy answers 502 and the
// live event stream drops until it reconnects.
test.use({ expectedConsoleError: /status of 50\d|net::ERR_/ })

test('an admin changes settings live and restarts the server for the rest', async ({ page }) => {
  await page.goto('/admin/config/accounts')

  // `make e2e` sets registration in the environment, which locks it here.
  await expect(page.getByText('Set by POLYTHOUGHT_REGISTRATION')).toBeVisible()
  await expect(page.getByLabel('Registration')).toBeDisabled()

  // A live setting applies at once: the password form reads the new rule.
  await page.getByLabel('Shortest password').fill('20')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()

  await page.goto('/settings/account')
  await expect(page.getByText('At least 20 characters.')).toBeVisible()

  // Back to the default, for the tests that create accounts.
  await page.goto('/admin/config/accounts')
  await page.getByRole('button', { name: 'Reset to default' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Settings saved')).toBeVisible()
  await expect(page.getByLabel('Shortest password')).toHaveValue('')

  // One that applies on restart waits for it.
  const menu = page.getByRole('navigation', { name: 'Config pages' })
  await menu.getByRole('link', { name: 'Logging' }).click()
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
  await menu.getByRole('link', { name: 'History' }).click()
  const history = page.getByRole('table')
  await expect(history.getByRole('row', { name: /Log format/ })).toContainText('text → json')
  await expect(history.getByRole('row', { name: /Shortest password/ })).toHaveCount(2)
})

test('the limits on user code are settings, checked when saved', async ({ page }) => {
  await page.goto('/admin/config/code')
  await expect(page.getByRole('heading', { name: 'Code' })).toBeVisible()
  await expect(page.getByLabel('Memory per run (MiB)')).toBeVisible()

  await page.getByLabel('Time per run').fill('10m')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText(/must be between 100ms and 5m/)).toBeVisible()
})

test('a setting is found from any Config page, and its page opens at it (D391)', async ({
  page,
}) => {
  await page.goto('/admin/config')
  await expect(page.getByRole('heading', { name: 'Polymarket access' })).toBeVisible()

  await page.keyboard.press('/')
  const search = page.getByRole('searchbox', { name: 'Search settings' })
  await expect(search).toBeFocused()
  await search.fill('mail.port') // by key
  await search.press('Enter')

  await expect(page).toHaveURL(/\/admin\/config\/mail#mail\.port$/)
  const port = page.getByLabel('Port', { exact: true })
  await expect(port).toBeFocused()
  await expect(port).toBeInViewport()

  // A change kept as another page opens, marked in the menu; leaving Config asks.
  await port.fill('2525')
  await search.press('Escape')
  const menu = page.getByRole('navigation', { name: 'Config pages' })
  await menu.getByRole('link', { name: 'Network' }).click()
  await expect(menu.getByRole('link', { name: 'Mail, 1 unsaved' })).toBeVisible()

  await page
    .getByRole('navigation', { name: 'Pages', exact: true })
    .getByRole('link', { name: 'Workflows' })
    .click()
  const asked = page.getByRole('alertdialog', { name: 'Leave without saving?' })
  await expect(asked).toContainText('1 unsaved change on the Config pages will be lost.')
  await asked.getByRole('button', { name: 'Stay' }).click()
  await expect(page).toHaveURL(/\/admin\/config\/network$/)

  await menu.getByRole('link', { name: 'Mail, 1 unsaved' }).click()
  await expect(port).toHaveValue('2525')
  await page.getByRole('button', { name: 'Discard' }).click()
  await expect(menu.getByRole('link', { name: 'Mail', exact: true })).toBeVisible()
})
