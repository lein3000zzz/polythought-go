import { expect, test, unique } from './fixtures'

// Mailpit, in the e2e stack, takes the mail and shows it here.
const mailpit = 'http://localhost:3125/api/v1'

type Messages = { messages: { Subject: string; To: { Address: string }[] }[] }

test('a test email goes through the mail server set up on the Config page', async ({ page }) => {
  const to = `${unique('ops')}@e2e.test`.toLowerCase()

  const mail = (on: boolean) =>
    page.request.patch('/api/v1/admin/settings', {
      data: {
        values: {
          'mail.host': on ? 'mailpit' : null,
          'mail.port': on ? 1025 : null,
          'mail.security': on ? 'none' : null,
          'mail.from_address': on ? 'polythought@e2e.test' : null,
        },
      },
    })

  await page.goto('/admin/config/mail')
  await expect(page.getByText('Save a mail server and a From address first.')).toBeVisible()

  try {
    const set = await mail(true)
    expect(set.status(), await set.text()).toBe(200)

    await page.reload()
    await page.getByLabel('Send a test email to').fill(to)
    await page.getByRole('button', { name: 'Send test email' }).click()
    await expect(page.getByText(`Sent to ${to}`)).toBeVisible()

    await expect
      .poll(async () => {
        const got = (await (await page.request.get(`${mailpit}/messages`)).json()) as Messages
        return got.messages.find((m) => m.To.some((r) => r.Address === to))?.Subject
      })
      .toBe('Polythought test email')
  } finally {
    // Back to the defaults, for the other tests.
    const reset = await mail(false)
    expect(reset.status(), await reset.text()).toBe(200)
  }
})
