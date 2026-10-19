import { randomBytes } from 'node:crypto'

import { expect, test, unique } from './fixtures'

// Anvil's first key: well known, never funded. It controls none of the wallet's addresses.
const anvilKey = '0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80'
const anvilAddress = '0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266'

test('a private key is refused when it does not control the wallet, and only while keys are taken', async ({
  page,
}) => {
  const label = unique('keys')
  const linked = await page.request.post('/api/v1/wallets', {
    data: { address: `0x${randomBytes(20).toString('hex')}`, label },
  })
  expect(linked.status(), await linked.text()).toBe(201)

  await page.goto('/wallets')
  const card = page.locator('[data-slot="card"]').filter({ hasText: label })

  // The instance is invite-only, so Private keys, left on auto, takes them (D21).
  await card.getByRole('button', { name: 'Import private key' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Private key').fill(anvilKey)
  await dialog.getByRole('button', { name: 'Import' }).click()

  // Checked before anything is asked of Polymarket (D155), saying whose key it is.
  await expect(dialog.getByText(/This key does not control the wallet/)).toBeVisible()
  await expect(dialog.getByText(new RegExp(anvilAddress))).toBeVisible()
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(card.getByText('Can trade')).toHaveCount(0)

  try {
    await page.goto('/admin/config')
    await page.getByLabel('Private keys').selectOption('off')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Settings saved')).toBeVisible()

    await page.goto('/wallets')
    await expect(card.getByRole('button', { name: 'Add credentials' })).toBeVisible()
    await expect(card.getByRole('button', { name: 'Import private key' })).toHaveCount(0)
  } finally {
    // Back to the default, for the other tests.
    const reset = await page.request.patch('/api/v1/admin/settings', {
      data: { values: { 'trading.key_import': null } },
    })
    expect(reset.status(), await reset.text()).toBe(200)
  }
})
