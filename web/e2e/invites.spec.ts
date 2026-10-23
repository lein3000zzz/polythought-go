import { expect, signedOut, test, unique } from './fixtures'

// Mailpit, in the e2e stack, takes the mail; the tests read it from there.
const mailpit = 'http://localhost:3125/api/v1'

type Summary = { ID: string; Subject: string; To: { Address: string }[] }

test('an invite mailed to an address signs that address up, verified', async ({
  page,
  browser,
}) => {
  const email = `${unique('invited')}@e2e.test`.toLowerCase()

  const settings = (values: Record<string, unknown>) =>
    page.request.patch('/api/v1/admin/settings', { data: { values } })
  const mail = {
    'mail.host': 'mailpit',
    'mail.port': 1025,
    'mail.security': 'none',
    'mail.from_address': 'polythought@e2e.test',
  }

  try {
    // Sign-ups verify, but a mailed invite proves its address (D181).
    const on = await settings({ ...mail, 'auth.verify_email': true })
    expect(on.status(), await on.text()).toBe(200)

    await page.goto('/admin/invites')
    await page.getByLabel('Invite by email').fill(email)
    await page.getByRole('button', { name: 'Send invite' }).click()
    await expect(page.getByText(`Mailed to ${email}`, { exact: true })).toBeVisible()

    // Not to an address with an account (D183).
    await page.getByLabel('Invite by email').fill('admin@e2e.test')
    await page.getByRole('button', { name: 'Send invite' }).click()
    await expect(page.getByText(/admin@e2e\.test has an account already/)).toBeVisible()

    let id = ''
    await expect
      .poll(async () => {
        const list = (await (await page.request.get(`${mailpit}/messages`)).json()) as {
          messages: Summary[]
        }
        id = list.messages.find((m) => m.To.some((r) => r.Address === email))?.ID ?? ''
        return id
      })
      .not.toBe('')

    const message = (await (await page.request.get(`${mailpit}/message/${id}`)).json()) as {
      Subject: string
      Text: string
    }
    expect(message.Subject).toBe('admin@e2e.test invited you to Polythought') // D182
    const link = new URL(/https?:\/\/\S+\/register\?invite=\S+/.exec(message.Text)![0])

    const context = await browser.newContext({ storageState: signedOut })
    const visitor = await context.newPage()

    await visitor.goto(link.pathname + link.search)
    await expect(visitor.getByLabel('Email')).toHaveValue(email)
    await expect(visitor.getByLabel('Email')).toHaveAttribute('readonly', '')
    await visitor.getByLabel('Password').fill('a long enough password')
    await visitor.getByRole('button', { name: 'Create account' }).click()

    // Signed straight in: no verification link to wait for.
    await expect(visitor).toHaveURL(/\/workflows/)
    await context.close()

    await page.reload()
    await expect(page.getByRole('cell', { name: email }).first()).toBeVisible()
  } finally {
    // Back to the defaults, for the other tests.
    const reset = await settings({
      'auth.verify_email': null,
      ...Object.fromEntries(Object.keys(mail).map((k) => [k, null])),
    })
    expect(reset.status(), await reset.text()).toBe(200)
  }
})

test('a typed code admits as many sign-ups as it allows, any case, and says who (D371)', async ({
  page,
  browser,
}) => {
  const code = unique('E2E-Spring')
  const people = [1, 2, 3].map((n) => `${unique('multi')}-${n}@e2e.test`.toLowerCase())

  await page.goto('/admin/invites')
  await page.getByLabel('Code').fill(code)
  await expect(page.getByText('Free to take.')).toBeVisible()
  await page.getByLabel('Sign-ups').fill('2')
  await page.getByRole('button', { name: 'Make invite' }).click()
  await expect(page.getByText('Invite made: share its link')).toBeVisible()
  await expect(page.getByText(/It admits 2 sign-ups/)).toBeVisible()

  // Any case is the same code: it is taken now (D393).
  await page.getByLabel('Code').fill(code.toUpperCase())
  await expect(page.getByText('Another invite has this code.')).toBeVisible()
  await page.getByLabel('Code').fill('')

  // Through the API, which signs no one in: sign-ins are limited per address, and every
  // test's browser has the same one. The mailed invite's test signs up on the page.
  const context = await browser.newContext({ storageState: signedOut })
  const register = (email: string) =>
    context.request.post('/api/v1/auth/register', {
      data: { email, password: 'a long enough password', inviteCode: code.toLowerCase() },
    })
  expect((await register(people[0])).status()).toBe(201)
  expect((await register(people[1])).status()).toBe(201)
  const third = await register(people[2])
  expect(third.status()).toBe(400)
  expect(await third.text()).toContain('invalid_invite')
  await context.close()

  await page.reload()
  const row = page.getByRole('row', { name: new RegExp(code) })
  await expect(row).toContainText('Used up')
  await row.getByRole('button', { name: '2 of 2' }).click()
  const who = page.getByRole('list', { name: `Who signed up with ${code}` })
  await expect(who).toContainText(people[0])
  await expect(who).toContainText(people[1])
})

test('a revoked invite admits no one, and stays listed', async ({ page, browser }) => {
  const code = unique('E2E-Revoked')

  await page.goto('/admin/invites')
  await page.getByLabel('Code').fill(code)
  await expect(page.getByText('Free to take.')).toBeVisible()
  await page.getByRole('button', { name: 'Make invite' }).click()
  await expect(page.getByText('Invite made: share its link')).toBeVisible()

  const row = page.getByRole('row', { name: new RegExp(code) })
  await row.getByRole('button', { name: 'Revoke', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Revoke' }).click()
  await expect(row).toContainText('Revoked')

  const context = await browser.newContext({ storageState: signedOut })
  const visitor = await context.newPage()
  await visitor.goto(`/register?invite=${code}`)
  await visitor.getByLabel('Email').fill(`${unique('late')}@e2e.test`.toLowerCase())
  await visitor.getByLabel('Password').fill('a long enough password')
  await visitor.getByRole('button', { name: 'Create account' }).click()
  await expect(visitor.getByText(/invalid or expired invite/i)).toBeVisible()
  await context.close()
})
