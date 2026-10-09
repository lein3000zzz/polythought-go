import { createWorkflow, expect, signedOut, signIn, test, unique } from './fixtures'

test('an invited user, disabled and enabled again, comes back to stopped workflows', async ({
  page,
  browser,
}) => {
  const person = { email: `${unique('invited')}@e2e.test`, password: 'invited-password-123' }

  await page.goto('/admin')
  await page.getByRole('button', { name: 'Create invite' }).click()
  const link = (await page.locator('code', { hasText: '/register?invite=' }).innerText()).trim()

  // The invited person registers in their own browser and starts a workflow.
  const theirs = await browser.newContext({ storageState: signedOut }) // not the admin's
  const them = await theirs.newPage()
  await them.goto(link)
  await them.getByLabel('Email').fill(person.email)
  await them.getByLabel('Password').fill(person.password)
  await them.getByRole('button', { name: 'Create account' }).click()
  await expect(them.getByRole('heading', { name: 'Workflows' })).toBeVisible()

  const id = await createWorkflow(them, unique('hourly'), [
    { id: 'every', type: 'trigger.schedule', params: { interval: '1h', runOnStart: false } },
    { id: 'log', type: 'output.log' },
  ])
  expect((await them.request.post(`/api/v1/workflows/${id}/activate`)).status()).toBe(200)

  // The invite is used up.
  await page.reload()
  await expect(page.getByText(/^Used /)).toBeVisible()

  // Disabling signs them out and stops their workflows.
  const row = page.getByRole('row', { name: new RegExp(person.email) })
  await row.getByRole('button', { name: 'Disable' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Disable' }).click()
  await expect(row.getByText('Disabled')).toBeVisible()

  await them.reload()
  await expect(them).toHaveURL(/\/login/)
  await them.getByLabel('Email').fill(person.email)
  await them.getByLabel('Password').fill(person.password)
  await them.getByRole('button', { name: 'Sign in' }).click()
  await expect(them.getByText('Account disabled')).toBeVisible()

  // Enabled again, they find the workflow switched off.
  await row.getByRole('button', { name: 'Enable' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Enable' }).click()
  await expect(row.getByText('Active')).toBeVisible()

  await signIn(them, person)
  const listed = them.getByRole('row', { name: /hourly/ })
  await expect(listed.getByText('Inactive')).toBeVisible()

  await theirs.close()
})
