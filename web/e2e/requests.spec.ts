import type { Page } from '@playwright/test'

import { expect, signedOut, test, unique } from './fixtures'

// Mailpit, in the e2e stack, takes the mail; the tests read it from there.
const mailpit = 'http://localhost:3125/api/v1'

type Summary = { ID: string; Subject: string; To: { Address: string }[] }

/** The text of the mail with subject to the address, once it came. */
async function mailTo(page: Page, address: string, subject: string): Promise<string> {
  let id = ''
  await expect
    .poll(async () => {
      const list = (await (await page.request.get(`${mailpit}/messages`)).json()) as {
        messages: Summary[]
      }
      id =
        list.messages.find((m) => m.Subject === subject && m.To.some((r) => r.Address === address))
          ?.ID ?? ''
      return id
    })
    .not.toBe('')

  return ((await (await page.request.get(`${mailpit}/message/${id}`)).json()) as { Text: string })
    .Text
}

test('someone asks to join, confirms by mail, and an approval mails them an invite (D394)', async ({
  page,
  browser,
}) => {
  const asking = `${unique('asking')}@e2e.test`.toLowerCase()
  const denied = `${unique('denied')}@e2e.test`.toLowerCase()

  const settings = (values: Record<string, unknown>) =>
    page.request.patch('/api/v1/admin/settings', { data: { values } })
  const mail = {
    'mail.host': 'mailpit',
    'mail.port': 1025,
    'mail.security': 'none',
    'mail.from_address': 'polythought@e2e.test',
  }

  try {
    const on = await settings({ ...mail, 'auth.requests': true })
    expect(on.status(), await on.text()).toBe(200)

    const context = await browser.newContext({ storageState: signedOut })
    const visitor = await context.newPage()

    const ask = async (email: string, note: string) => {
      await visitor.goto('/register')
      await visitor.getByRole('link', { name: 'Ask for one' }).click()
      // The sign-up page has an Email field too: fill in the request's, once it is there.
      await expect(visitor).toHaveURL(/\/request-access$/)
      await expect(visitor.getByLabel('Note (optional)')).toBeVisible()
      await visitor.getByLabel('Email').fill(email)
      await visitor.getByLabel('Note (optional)').fill(note)
      await visitor.getByRole('button', { name: 'Ask for an invite' }).click()
      await expect(visitor.getByText('Check your inbox')).toBeVisible()

      // Nothing reaches the admins before the address is confirmed.
      const text = await mailTo(page, email, 'Confirm your request to join Polythought')
      const link = new URL(/https?:\/\/\S+\/request-access\?token=\S+/.exec(text)![0])
      await visitor.goto(link.pathname + link.search)
      await expect(visitor.getByText('Your request is with the admins')).toBeVisible()
    }

    await ask(asking, 'I trade weather markets.')
    await ask(denied, 'Let me in.')

    // Counted on the tab, from any admin page.
    await page.goto('/admin')
    const tab = page.getByRole('navigation', { name: 'Admin pages' }).getByRole('link', {
      name: /^Invites, \d+ requests waiting$/,
    })
    await expect(tab).toBeVisible()
    await tab.click()

    const requests = page.getByRole('list', { name: 'Requests to join' })
    await expect(requests).toContainText('I trade weather markets.')
    await requests.getByRole('button', { name: `Approve ${asking}` }).click()
    await expect(page.getByText(`Mailed to ${asking}`, { exact: true })).toBeVisible()

    await requests.getByRole('button', { name: `Deny ${denied}` }).click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Deny' }).click()
    await expect(page.getByText('Request denied')).toBeVisible()

    // The invite is the address's alone: it signs up with it, through the API, which signs
    // no one in (sign-ins are limited per address, and every test's browser has one).
    const invite = await mailTo(page, asking, 'admin@e2e.test invited you to Polythought')
    const code = new URL(/https?:\/\/\S+\/register\?invite=\S+/.exec(invite)![0]).searchParams.get(
      'invite',
    )!
    const other = await context.request.post('/api/v1/auth/register', {
      data: { email: denied, password: 'a long enough password', inviteCode: code },
    })
    expect(other.status(), 'another address').toBe(400)
    const signedUp = await context.request.post('/api/v1/auth/register', {
      data: { email: asking, password: 'a long enough password', inviteCode: code },
    })
    expect(signedUp.status(), await signedUp.text()).toBe(201)

    // The one denied is told so, while Tell denied requests is on, as it is to start with.
    expect(await mailTo(page, denied, 'Your request to join Polythought')).toContain(
      'ask again in 30 days',
    )
    await context.close()
  } finally {
    // Back to the defaults, for the other tests.
    const reset = await settings({
      'auth.requests': null,
      ...Object.fromEntries(Object.keys(mail).map((k) => [k, null])),
    })
    expect(reset.status(), await reset.text()).toBe(200)
  }
})
