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

    await page.goto('/admin')
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
