import { expect, test } from './fixtures'

test('the Config page says whether Polymarket takes orders from the server, and checks again', async ({
  page,
}) => {
  await page.goto('/admin/config')
  await expect(page.getByRole('heading', { name: 'Polymarket access' })).toBeVisible()

  // Polymarket answers for wherever the test stack runs: either way, it is said.
  await page.getByRole('button', { name: 'Check now' }).click()
  await expect(page.getByText(/^Answered /)).toBeVisible({ timeout: 15_000 })
  await expect(
    page
      .getByText('Polymarket blocks orders from this server')
      .or(page.getByText(/^Polymarket takes orders from this server/)),
  ).toBeVisible()
})
