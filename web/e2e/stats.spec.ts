import { admin, createWorkflow, expect, test, unique } from './fixtures'

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

test("an admin sees every user's workflows, and opens one user's (D431)", async ({ page }) => {
  const name = unique('counted')
  const id = await createWorkflow(page, name, [{ id: 'manual', type: 'trigger.manual' }])
  expect((await page.request.post(`/api/v1/workflows/${id}/run`)).status()).toBe(200)

  await page.goto('/admin')
  await page
    .getByRole('navigation', { name: 'Admin pages' })
    .getByRole('link', { name: 'Stats' })
    .click()
  await expect(page).toHaveURL(/\/admin\/stats/)
  await expect(page.getByRole('region', { name: 'Totals' })).toBeVisible()

  const users = page.getByRole('table', { name: 'Users' })
  await users.getByRole('link', { name: admin.email }).click()
  await expect(page.getByRole('heading', { name: `${admin.email}'s workflows` })).toBeVisible()

  const workflows = page.getByRole('table', { name: 'Workflows' })
  await expect(workflows.getByText(name, { exact: true })).toBeVisible()
  await expect(workflows.getByRole('link', { name })).toHaveCount(0)
})

test('an admin switches off a busy workflow and holds it off, then releases it (D435)', async ({
  page,
}) => {
  const name = unique('busy')
  const id = await createWorkflow(page, name, [
    { id: 'every', type: 'trigger.schedule', params: { interval: '1h', runOnStart: false } },
    { id: 'log', type: 'output.log' },
  ])
  expect((await page.request.post(`/api/v1/workflows/${id}/activate`)).status()).toBe(200)

  await page.goto('/admin/stats?tab=workflows')
  const table = page.getByRole('table', { name: "Every user's workflows" })
  const row = table.getByRole('row').filter({ has: page.getByRole('link', { name, exact: true }) })
  await row.getByRole('button', { name: 'Switch off' }).click()

  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Why').fill('It ran too often.')
  await dialog.getByRole('button', { name: 'Switch off' }).click()
  await expect(row.getByText('Held')).toBeVisible()

  // Its owner cannot switch it on again, and is told why.
  await page.goto(`/workflows/${id}`)
  await expect(page.getByText('Held by an admin')).toBeVisible()
  await expect(page.getByRole('switch', { name: 'Active' })).toBeDisabled()
  const refused = await page.request.post(`/api/v1/workflows/${id}/activate`)
  expect(refused.status()).toBe(409)

  await page.getByRole('button', { name: 'History' }).click()
  const panel = page.getByRole('region', { name: 'History' })
  await panel.getByRole('tab', { name: 'History' }).click()
  await expect(panel.getByText('Held off')).toBeVisible()
  await expect(panel.getByText(/by an admin: It ran too often\./)).toBeVisible()

  // Released, it switches on again.
  await page.goto('/admin/stats?tab=workflows')
  await row.getByRole('button', { name: 'Release' }).click()
  await expect(row.getByRole('button', { name: 'Hold off' })).toBeVisible()
  expect((await page.request.post(`/api/v1/workflows/${id}/activate`)).status()).toBe(200)
})
