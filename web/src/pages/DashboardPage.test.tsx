import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const counts = { runs: 0, failed: 0 }
const result = (more: Partial<Schemas['TradingResult']>): Schemas['TradingResult'] => ({
  mode: 'paper',
  profit: '0',
  realized: '0',
  unrealized: '0',
  positions: 0,
  unpriced: 0,
  volume: '0',
  orders: 0,
  wins: 0,
  closed: 0,
  ...more,
})
const totals = (more: Partial<Schemas['StatsTotals']>): Schemas['StatsTotals'] => ({
  workflows: 0,
  profit: '0',
  realized: '0',
  unrealized: '0',
  positions: 0,
  unpriced: 0,
  volume: '0',
  orders: 0,
  wins: 0,
  closed: 0,
  ...more,
})

const overview: Schemas['WorkflowsOverview'] = {
  workflows: [
    {
      id: 'wf-1',
      name: 'Copy Alice',
      active: true,
      trading: result({
        profit: '12.4',
        realized: '10',
        unrealized: '2.4',
        positions: 3,
        volume: '90',
        wins: 3,
        closed: 4,
      }),
      runs: { day: { runs: 4, failed: 1 }, week: { runs: 30, failed: 2 } },
    },
    {
      id: 'wf-2',
      name: 'Backtest',
      active: false,
      trading: result({ profit: '-2.5', realized: '-2.5', volume: '10', closed: 1 }),
      runs: { day: counts, week: { runs: 3, failed: 0 } },
    },
    {
      id: 'wf-3',
      name: 'Live one',
      active: true,
      trading: result({ mode: 'live', profit: '1.25', unrealized: '1.25', positions: 1 }),
      runs: { day: counts, week: counts },
    },
    {
      id: 'wf-4',
      name: 'Alerts',
      active: true,
      runs: { day: { runs: 9, failed: 0 }, week: { runs: 50, failed: 0 } },
    },
  ],
  traders: [
    {
      mode: 'paper',
      trader: '0x00000000000000000000000000000000000000a1',
      label: 'Alice',
      workflows: 2,
      profit: '9',
      realized: '8',
      unrealized: '1',
      positions: 2,
      volume: '80',
      orders: 6,
      wins: 2,
      closed: 3,
    },
    {
      mode: 'paper',
      workflows: 1,
      profit: '0.9',
      realized: '0.9',
      unrealized: '0',
      positions: 0,
      volume: '20',
      orders: 1,
      wins: 0,
      closed: 0,
    },
    {
      mode: 'live',
      trader: '0x00000000000000000000000000000000000000b2',
      label: 'Bob',
      workflows: 1,
      profit: '1.25',
      realized: '0',
      unrealized: '1.25',
      positions: 1,
      volume: '5',
      orders: 1,
      wins: 0,
      closed: 0,
    },
  ],
  paper: totals({
    workflows: 2,
    profit: '9.9',
    realized: '7.5',
    unrealized: '2.4',
    positions: 3,
    volume: '100',
    orders: 9,
    wins: 3,
    closed: 5,
  }),
  live: totals({
    workflows: 1,
    profit: '1.25',
    unrealized: '1.25',
    positions: 1,
    volume: '5',
    orders: 1,
  }),
  day: { runs: 13, failed: 1 },
  week: { runs: 83, failed: 2 },
  at: '2026-10-28T12:00:00Z',
}

// dashboard serves the overview, and alice with the parts she hid, keeping what she sets.
function dashboard(hidden: string[] = []) {
  const set: string[][] = []

  server.use(
    http.get('/api/v1/me', () => HttpResponse.json({ ...alice, dashboard: { hidden } })),
    http.get('/api/v1/workflows/stats', () => HttpResponse.json(overview)),
    http.put('/api/v1/me/dashboard', async ({ request }) => {
      const body = (await request.json()) as { hidden: string[] }
      set.push(body.hidden)
      hidden = body.hidden

      return new HttpResponse(null, { status: 204 })
    }),
  )

  return set
}

// The value of one of the totals' figures, by its label.
const figure = (totals: HTMLElement, label: string) =>
  within(totals).getByText(label, { selector: 'dt' }).nextElementSibling

const rowNames = (table: HTMLElement) =>
  within(table)
    .getAllByRole('row')
    .slice(1)
    .map((r) => within(r).getAllByRole('cell')[0].textContent)

describe('dashboard', () => {
  it('shows the totals, every workflow and the traders, live where anything trades live (D429)', async () => {
    dashboard()
    renderApp('/workflows/dashboard')

    const totals = await screen.findByRole('region', { name: 'Totals' })
    expect(screen.getByRole('button', { name: 'Live' })).toHaveAttribute('aria-pressed', 'true')
    expect(figure(totals, 'Profit')).toHaveTextContent('+$1.25')
    expect(within(totals).getByText('1 workflow live')).toBeInTheDocument()

    // Those trading live first, the rest after, as they came.
    const workflows = screen.getByRole('table', { name: 'Workflows' })
    expect(rowNames(workflows)).toEqual([
      'Live oneActive',
      'Copy AliceActive',
      'Backtest',
      'AlertsActive',
    ])
    expect(within(workflows).getByRole('link', { name: 'Live one' })).toHaveAttribute(
      'href',
      '/workflows/wf-3?view=trading',
    )
    expect(within(workflows).getByRole('link', { name: 'Alerts' })).toHaveAttribute(
      'href',
      '/workflows/wf-4',
    )

    const traders = screen.getByRole('table', { name: 'Traders' })
    expect(rowNames(traders), 'live ones alone').toEqual(['Bob'])
  })

  it('switches to paper, and the URL says so', async () => {
    dashboard()
    const { router, user } = renderApp('/workflows/dashboard')

    await user.click(await screen.findByRole('button', { name: 'Paper' }))

    const totals = screen.getByRole('region', { name: 'Totals' })
    expect(figure(totals, 'Profit')).toHaveTextContent('+$9.90')
    expect(figure(totals, 'Win rate')).toHaveTextContent('60%')
    expect(within(totals).getByText('3 of 5 closed made money')).toBeInTheDocument()

    expect(rowNames(screen.getByRole('table', { name: 'Traders' }))).toEqual([
      'Alice',
      'Copied from no one',
    ])
    expect(router.state.location.search).toBe('?mode=paper')
  })

  it('sorts the workflows by a column, either way, those with nothing last', async () => {
    dashboard()
    const { user } = renderApp('/workflows/dashboard?mode=paper')

    const workflows = await screen.findByRole('table', { name: 'Workflows' })
    expect(rowNames(workflows)).toEqual([
      'Copy AliceActive',
      'Backtest',
      'Live oneActive',
      'AlertsActive',
    ])

    const profit = within(workflows).getByRole('columnheader', { name: /Profit/ })
    expect(profit).toHaveAttribute('aria-sort', 'descending')

    await user.click(within(profit).getByRole('button'))
    expect(profit).toHaveAttribute('aria-sort', 'ascending')
    expect(rowNames(workflows).slice(0, 2)).toEqual(['Backtest', 'Copy AliceActive'])
    expect(rowNames(workflows).slice(2), 'nothing on paper: still last').toEqual([
      'Live oneActive',
      'AlertsActive',
    ])

    await user.click(within(workflows).getByRole('button', { name: 'Workflow' }))
    expect(rowNames(workflows)).toEqual([
      'AlertsActive',
      'Backtest',
      'Copy AliceActive',
      'Live oneActive',
    ])
  })

  it('hides a part, kept with the account', async () => {
    const set = dashboard()
    const { user } = renderApp('/workflows/dashboard')

    await screen.findByRole('region', { name: 'Traders' })
    await user.click(screen.getByRole('button', { name: 'Customize' }))
    await user.click(await screen.findByRole('menuitemcheckbox', { name: 'Traders' }))

    await waitFor(() => expect(set).toEqual([['traders']]))
    // The menu stays open for the next, hiding the page from queries while it is.
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('region', { name: 'Traders' })).not.toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Totals' })).toBeInTheDocument()
  })

  it('keeps hidden what the account hid, and says so when all are', async () => {
    dashboard(['totals', 'workflows', 'traders'])
    renderApp('/workflows/dashboard')

    expect(
      await screen.findByText('Every part is hidden: Customize shows them again.'),
    ).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Totals' })).not.toBeInTheDocument()
  })
})
