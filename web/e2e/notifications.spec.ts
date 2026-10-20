import { createWorkflow, expect, test, unique } from './fixtures'

// Mailpit, in the e2e stack, takes the mail; the tests read it from there.
const mailpit = 'http://localhost:3125/api/v1'

type Summary = { ID: string; Subject: string; To: { Address: string }[] }

test('a failing workflow is told by email, once the admin allows it and the user asks', async ({
  page,
}) => {
  const name = unique('failing')

  const settings = (values: Record<string, unknown>) =>
    page.request.patch('/api/v1/admin/settings', { data: { values } })
  const mail = {
    'mail.host': 'mailpit',
    'mail.port': 1025,
    'mail.security': 'none',
    'mail.from_address': 'polythought@e2e.test',
  }

  let id = ''

  try {
    const set = await settings(mail)
    expect(set.status(), await set.text()).toBe(200)

    // Off until an admin allows it (D184).
    await page.goto('/settings')
    const byEmail = page.getByRole('switch', { name: 'A workflow fails, by email' })
    await expect(byEmail).toBeDisabled()

    const allow = await settings({ 'mail.notifications': true })
    expect(allow.status(), await allow.text()).toBe(200)

    await page.reload()
    await expect(byEmail).not.toBeChecked() // until the user turns it on (D170)
    await byEmail.click()
    await expect(byEmail).toBeChecked()

    // Runs on a schedule, and fails: webhooks to private addresses are refused.
    id = await createWorkflow(page, name, [
      { id: 'every', type: 'trigger.schedule', params: { interval: '1s', runOnStart: true } },
      { id: 'hook', type: 'output.webhook', params: { url: 'http://127.0.0.1:9/hook' } },
    ])
    expect((await page.request.post(`/api/v1/workflows/${id}/activate`)).status()).toBe(200)

    let mailID = ''
    await expect
      .poll(
        async () => {
          const list = (await (await page.request.get(`${mailpit}/messages`)).json()) as {
            messages: Summary[]
          }
          mailID =
            list.messages.find(
              (m) =>
                m.Subject === `${name} failed` && m.To.some((r) => r.Address === 'admin@e2e.test'),
            )?.ID ?? ''
          return mailID
        },
        { timeout: 15_000 },
      )
      .not.toBe('')

    const message = (await (await page.request.get(`${mailpit}/message/${mailID}`)).json()) as {
      Text: string
    }
    expect(message.Text).toContain(`${name} failed.`)
    expect(message.Text).toContain('The error:')
    expect(message.Text).toContain(`/workflows/${id}`)
    expect(message.Text).toContain('is on by email in Settings → Notifications')
  } finally {
    if (id) await page.request.post(`/api/v1/workflows/${id}/deactivate`)

    await page.request.put('/api/v1/notifications/workflow_failed', { data: { email: false } })

    // Back to the defaults, for the other tests.
    const reset = await settings({
      'mail.notifications': null,
      ...Object.fromEntries(Object.keys(mail).map((k) => [k, null])),
    })
    expect(reset.status(), await reset.text()).toBe(200)
  }
})

test('a paper order that comes to rest is told by email, once its kind is switched on (D229, D232)', async ({
  page,
}) => {
  const name = unique('resting mail')

  const settings = (values: Record<string, unknown>) =>
    page.request.patch('/api/v1/admin/settings', { data: { values } })
  const mail = {
    'mail.host': 'mailpit',
    'mail.port': 1025,
    'mail.security': 'none',
    'mail.from_address': 'polythought@e2e.test',
    'mail.notifications': true,
  }

  try {
    const set = await settings(mail)
    expect(set.status(), await set.text()).toBe(200)

    // Off to start with, as every paper kind is; by email it is the user's choice.
    await page.goto('/settings')
    await expect(page.getByRole('heading', { name: 'Paper orders' })).toBeVisible()
    const rests = page.getByRole('switch', { name: 'A paper order rests on the book, by email' })
    await expect(rests).not.toBeChecked()
    await rests.click()
    await expect(rests).toBeChecked()

    const id = await createWorkflow(page, name, [
      { id: 'manual', type: 'trigger.manual' },
      { id: 'markets', type: 'polymarket.markets', params: { limit: 1 } },
      {
        id: 'buy',
        type: 'trading.placeOrder',
        params: {
          from: 'settings',
          tokenId: '{{ outcomes[0].tokenId }}',
          side: 'buy',
          price: '{{ tickSize }}',
          shares: '{{ minOrderSize }}',
          type: 'GTC',
        },
      },
    ])
    const ran = await page.request.post(`/api/v1/workflows/${id}/run`)
    expect(((await ran.json()) as { status: string }).status).toBe('succeeded')

    let mailID = ''
    await expect
      .poll(
        async () => {
          const list = (await (await page.request.get(`${mailpit}/messages`)).json()) as {
            messages: Summary[]
          }
          mailID =
            list.messages.find((m) => m.Subject === `Paper order of ${name} rests on the book`)
              ?.ID ?? ''
          return mailID
        },
        { timeout: 15_000 },
      )
      .not.toBe('')

    const message = (await (await page.request.get(`${mailpit}/message/${mailID}`)).json()) as {
      Text: string
    }
    expect(message.Text).toMatch(
      new RegExp(
        `Paper: ${name}'s buy of \\d+ shares of .+ in “.+” at [\\d.]+¢ rests on the book\\.`,
      ),
    )
    expect(message.Text).toContain(`/workflows/${id}`)

    // Its rest, cancelled, tells nothing: that kind is off.
    const orders = (await (await page.request.get(`/api/v1/workflows/${id}/orders`)).json()) as {
      orders: { id: string }[]
    }
    const cancelled = await page.request.post(
      `/api/v1/workflows/${id}/orders/${orders.orders[0].id}/cancel`,
    )
    expect(cancelled.status(), await cancelled.text()).toBe(200)
  } finally {
    await page.request.put('/api/v1/notifications/paper_order_rests', { data: { email: false } })

    const reset = await settings(Object.fromEntries(Object.keys(mail).map((k) => [k, null])))
    expect(reset.status(), await reset.text()).toBe(200)
  }
})
