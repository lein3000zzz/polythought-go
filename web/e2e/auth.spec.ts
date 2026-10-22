import { admin, expect, signedOut, signIn, test } from './fixtures'

test.use({ storageState: signedOut })

test('signing in and out', async ({ page }) => {
  await page.goto('/workflows')
  await expect(page).toHaveURL(/\/login\?next=%2Fworkflows$/)

  await page.getByLabel('Email').fill(admin.email)
  await page.getByLabel('Password').fill('not the password')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText('Invalid email or password')).toBeVisible()

  await page.getByLabel('Password').fill(admin.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page).toHaveURL(/\/workflows$/)
  await page.getByRole('button', { name: 'Account' }).click()
  const account = page.getByRole('menu')
  await expect(account.getByText(admin.email)).toBeVisible()

  await account.getByRole('menuitem', { name: 'Sign out' }).click()
  await expect(page).toHaveURL(/\/login/)

  // The session is gone on the server too.
  await page.goto('/workflows')
  await expect(page).toHaveURL(/\/login/)
})

test('pages carry a content security policy', async ({ page }) => {
  const res = await page.goto('/login')
  expect(res?.headers()['content-security-policy']).toContain("script-src 'self'")

  // The fixture fails the test on any violation while the app runs under it.
  await signIn(page, admin)
})
