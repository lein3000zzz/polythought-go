import { expect, signedOut, test, unique } from './fixtures'

// Mailpit, in the e2e stack, takes the mail; the tests read it from there.
const mailpit = 'http://localhost:3125/api/v1'

type Summary = { ID: string; Subject: string; To: { Address: string }[] }

test('a forgotten password is set again with a mailed link', async ({ page, browser }) => {
  const email = `${unique('forgot')}@e2e.test`.toLowerCase()
  const fresh = 'a brand new passphrase'

  const settings = (values: Record<string, unknown>) =>
    page.request.patch('/api/v1/admin/settings', { data: { values } })
  const mail = {
    'mail.host': 'mailpit',
    'mail.port': 1025,
    'mail.security': 'none',
    'mail.from_address': 'polythought@e2e.test',
  }

  // The mail to email with subject, once Mailpit has it.
  const mailed = async (subject: string) => {
    let id = ''
    await expect
      .poll(async () => {
        const list = (await (await page.request.get(`${mailpit}/messages`)).json()) as {
          messages: Summary[]
        }
        id =
          list.messages.find((m) => m.Subject === subject && m.To.some((r) => r.Address === email))
            ?.ID ?? ''
        return id
      })
      .not.toBe('')

    return (await (await page.request.get(`${mailpit}/message/${id}`)).json()) as { Text: string }
  }

  try {
    const on = await settings(mail)
    expect(on.status(), await on.text()).toBe(200)

    const made = await page.request.post('/api/v1/admin/users', {
      data: { email, password: 'the old forgotten password', role: 'user' },
    })
    expect(made.status(), await made.text()).toBe(201)

    const context = await browser.newContext({ storageState: signedOut })
    const visitor = await context.newPage()

    await visitor.goto('/login')
    await visitor.getByLabel('Email').fill(email)
    await visitor.getByRole('link', { name: 'Forgot password?' }).click()
    await expect(visitor.getByLabel('Email')).toHaveValue(email)
    await visitor.getByRole('button', { name: 'Send the link' }).click()
    await expect(visitor.getByText(`If ${email} has an account`)).toBeVisible()

    const message = await mailed('Set a new Polythought password')
    const link = new URL(/https?:\/\/\S+\/reset-password\?token=\S+/.exec(message.Text)![0])

    await visitor.goto(link.pathname + link.search)
    await visitor.getByLabel('New password').fill(fresh)
    await visitor.getByRole('button', { name: 'Set the password' }).click()

    // Then signing in, with the new password (D177).
    await expect(visitor.getByText('Your password is changed: sign in with it.')).toBeVisible()
    await expect(visitor.getByLabel('Email')).toHaveValue(email)
    await visitor.getByLabel('Password').fill(fresh)
    await visitor.getByRole('button', { name: 'Sign in' }).click()
    await expect(visitor).toHaveURL(/\/workflows/)

    // The owner is told, in case it was not them.
    await mailed('Your Polythought password was changed')

    // The link worked once.
    await visitor.goto(link.pathname + link.search)
    await visitor.getByLabel('New password').fill('yet another passphrase')
    await visitor.getByRole('button', { name: 'Set the password' }).click()
    await expect(visitor.getByRole('link', { name: 'Ask for a new link' })).toBeVisible()

    await context.close()
  } finally {
    // Back to the defaults, for the other tests.
    const reset = await settings(Object.fromEntries(Object.keys(mail).map((k) => [k, null])))
    expect(reset.status(), await reset.text()).toBe(200)
  }
})
