import { randomBytes } from 'node:crypto'

import { createWorkflow, expect, test, unique } from './fixtures'

type Failure = { error: { code: string } }

// Nothing here places a live order (D26): a wallet linked by its address alone has no key,
// so it cannot trade, and nothing is sent to Polymarket.
test('a workflow goes live only once Live trading is on, from a wallet that can trade', async ({
  page,
}) => {
  const settings = (on: boolean) =>
    page.request.patch('/api/v1/admin/settings', { data: { values: { 'trading.live': on } } })

  const id = await createWorkflow(page, unique('live'), [
    { id: 'manual', type: 'trigger.manual' },
    {
      id: 'buy',
      type: 'trading.placeOrder',
      params: { from: 'settings', tokenId: '1', side: 'buy', price: '0.5', shares: '5' },
    },
  ])

  const linked = await page.request.post('/api/v1/wallets', {
    data: { address: `0x${randomBytes(20).toString('hex')}`, label: unique('watch only') },
  })
  expect(linked.status(), await linked.text()).toBe(201)
  const wallet = (await linked.json()) as { id: string }

  const goLive = () =>
    page.request.put(`/api/v1/workflows/${id}/trading/setup`, {
      data: { mode: 'live', walletId: wallet.id, confirm: true },
    })

  try {
    // Off to start with (D192): every workflow trades on paper.
    await page.goto(`/workflows/${id}`)
    await page.getByRole('tab', { name: 'Trading' }).click()

    const how = page.getByRole('region', { name: 'How it trades' })
    await expect(how.getByText(/^Trades on paper/)).toBeVisible()
    await expect(how.getByText(/^Live: live trading is off on this server/)).toBeVisible()
    await expect(how.getByRole('button', { name: 'Trade live…' })).toBeDisabled()

    const off = await goLive()
    expect(off.status()).toBe(403)
    expect(((await off.json()) as Failure).error.code).toBe('live_trading_off')

    const on = await settings(true)
    expect(on.status(), await on.text()).toBe(200)

    // On, a wallet without a key still cannot trade (D153), and the dialog says so.
    await page.reload()
    await page.getByRole('tab', { name: 'Trading' }).click()
    await how.getByRole('button', { name: 'Trade live…' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByText('Trade live?')).toBeVisible()
    await expect(dialog.getByText(/None of your wallets can trade yet/)).toBeVisible()
    await expect(dialog.getByRole('button', { name: 'Switch to live' })).toBeDisabled()
    await dialog.getByRole('button', { name: 'Cancel' }).click()

    const cannot = await goLive()
    expect(cannot.status()).toBe(400)
    expect(((await cannot.json()) as Failure).error.code).toBe('wallet_cannot_trade')

    const unconfirmed = await page.request.put(`/api/v1/workflows/${id}/trading/setup`, {
      data: { mode: 'live', walletId: wallet.id },
    })
    expect(unconfirmed.status()).toBe(400)

    const setup = (await (
      await page.request.get(`/api/v1/workflows/${id}/trading/setup`)
    ).json()) as { mode: string; liveAvailable: boolean }
    expect(setup).toEqual(expect.objectContaining({ mode: 'paper', liveAvailable: true }))
  } finally {
    await settings(false)
  }
})
