import { createWorkflow, expect, test, unique } from './fixtures'

// Each card says how its workflow is doing (D429), as the server counts its runs.
test('a card says its workflow ran, and failed where it did', async ({ page }) => {
  const okName = unique('ran')
  const badName = unique('fails')

  const ok = await createWorkflow(page, okName, [{ id: 'manual', type: 'trigger.manual' }])
  const bad = await createWorkflow(page, badName, [
    { id: 'manual', type: 'trigger.manual' },
    {
      id: 'boom',
      type: 'code.transform',
      params: { source: 'export function run(): never { throw new Error("boom") }' },
    },
  ])

  const ran = await page.request.post(`/api/v1/workflows/${ok}/run`)
  expect(((await ran.json()) as { status: string }).status).toBe('succeeded')
  const failed = await page.request.post(`/api/v1/workflows/${bad}/run`)
  expect(((await failed.json()) as { status: string }).status).toBe('failed')

  await page.goto('/workflows')
  const search = page.getByRole('searchbox', { name: 'Search workflows' })
  const card = (name: string) =>
    page.getByRole('listitem').filter({ has: page.getByRole('link', { name, exact: true }) })

  await search.fill(okName)
  await expect(card(okName)).toContainText('ran just now')

  await search.fill(badName)
  await expect(card(badName)).toContainText('failed just now')
  await expect(card(badName).getByTitle('The last run failed')).toBeVisible()
})

test('the dashboard shows the parts asked, kept with the account (D429)', async ({ page }) => {
  await page.goto('/workflows')
  await page.getByRole('link', { name: 'Dashboard' }).click()
  await expect(page).toHaveURL(/\/workflows\/dashboard/)
  await expect(page.getByRole('region', { name: 'Totals' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Traders' })).toBeVisible()

  await page.getByRole('button', { name: 'Customize' }).click()
  await page.getByRole('menuitemcheckbox', { name: 'Traders' }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('region', { name: 'Traders' })).toBeHidden()

  // Kept, so another visit has it so.
  await page.reload()
  await expect(page.getByRole('region', { name: 'Totals' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Traders' })).toBeHidden()

  await page.getByRole('button', { name: 'Customize' }).click()
  await page.getByRole('menuitemcheckbox', { name: 'Traders' }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('region', { name: 'Traders' })).toBeVisible()
})
