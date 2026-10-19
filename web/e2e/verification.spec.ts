import { expect, signedOut, test, unique } from './fixtures'

// Mailpit, in the e2e stack, takes the mail; the tests read it from there.
const mailpit = 'http://localhost:3125/api/v1'

type Summary = { ID: string; Subject: string; To: { Address: string }[] }

test('a sign-up verifies its address by mail before it signs in', async ({ page, browser }) => {
  const email = `${unique('new')}@e2e.test`.toLowerCase()
  const password = 'a long enough password'

  const settings = (values: Record<string, unknown>) =>
    page.request.patch('/api/v1/admin/settings', { data: { values } })
  const mail = {
    'mail.host': 'mailpit',
    'mail.port': 1025,
    'mail.security': 'none',
    'mail.from_address': 'polythought@e2e.test',
  }

  // Not without mail (D173).
  const refused = await settings({ 'auth.verify_email': true })
  expect(refused.status()).toBe(400)
  expect(await refused.text()).toContain('needs mail set up first')

  try {
    const on = await settings({ ...mail, 'auth.verify_email': true })
    expect(on.status(), await on.text()).toBe(200)

    const invite = await page.request.post('/api/v1/admin/invites')
    expect(invite.status(), await invite.text()).toBe(201)
    const { code } = (await invite.json()) as { code: string }

    // Someone else, signed out, in a browser of their own.
    const context = await browser.newContext({ storageState: signedOut })
    const visitor = await context.newPage()

    await visitor.goto(`/register?invite=${code}`)
    await visitor.getByLabel('Email').fill(email)
    await visitor.getByLabel('Password').fill(password)
    await visitor.getByRole('button', { name: 'Create account' }).click()
    await expect(visitor.getByText('Check your inbox')).toBeVisible()

    // Signing in first is refused, saying why (D166).
    await visitor.goto('/login')
    await visitor.getByLabel('Email').fill(email)
    await visitor.getByLabel('Password').fill(password)
    await visitor.getByRole('button', { name: 'Sign in' }).click()
    await expect(
      visitor.getByText('Verify your email first: open the link we mailed you'),
    ).toBeVisible()

    // The link in the mail.
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
    expect(message.Subject).toBe('Verify your email for Polythought')
    const link = /https?:\/\/\S+\/verify-email\?token=\S+/.exec(message.Text)?.[0]
    expect(link).toBeTruthy()

    // It verifies, then asks for the password (D172).
    await visitor.goto(new URL(link!).pathname + new URL(link!).search)
    await expect(visitor.getByText('Your address is verified: sign in.')).toBeVisible()
    await expect(visitor.getByLabel('Email')).toHaveValue(email)
    await visitor.getByLabel('Password').fill(password)
    await visitor.getByRole('button', { name: 'Sign in' }).click()
    await expect(visitor).toHaveURL(/\/workflows/)

    // The link worked once.
    await visitor.goto(new URL(link!).pathname + new URL(link!).search)
    await expect(visitor.getByText('This link does not work', { exact: true })).toBeVisible()

    await context.close()
  } finally {
    // Back to the defaults, for the other tests.
    const reset = await settings({
      'auth.verify_email': null,
      ...Object.fromEntries(Object.keys(mail).map((k) => [k, null])),
    })
    expect(reset.status(), await reset.text()).toBe(200)
  }
})
