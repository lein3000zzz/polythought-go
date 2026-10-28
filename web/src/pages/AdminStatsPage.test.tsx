import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const root: Schemas['User'] = { ...alice, id: 'u-0', email: 'root@example.com', role: 'admin' }

const totals = (more: Partial<Schemas['StatsTotals']> = {}): Schemas['StatsTotals'] => ({
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
const counts = { runs: 0, failed: 0 }

const user = (more: Partial<Schemas['UserTotals']>): Schemas['UserTotals'] => ({
  id: 'u-1',
  email: 'alice@example.com',
  role: 'user',
  disabled: false,
  workflows: 0,
  active: 0,
  paper: totals(),
  live: totals(),
  day: counts,
  week: counts,
  ...more,
})

const instance: Schemas['InstanceStats'] = {
  users: [
    user({
      workflows: 4,
      active: 2,
      paper: totals({
        workflows: 2,
        profit: '9.9',
        realized: '7.5',
        volume: '100',
        wins: 3,
        closed: 5,
      }),
      week: { runs: 30, failed: 2 },
    }),
    user({
      id: 'u-2',
      email: 'bob@example.com',
      disabled: true,
      workflows: 1,
      paper: totals({ workflows: 1, profit: '-4', realized: '-4', closed: 1 }),
    }),
    user({ id: 'u-0', email: 'root@example.com', role: 'admin', workflows: 2 }),
  ],
  paper: totals({
    workflows: 3,
    profit: '5.9',
    realized: '3.5',
    volume: '100',
    wins: 3,
    closed: 6,
  }),
  live: totals(),
  day: { runs: 4, failed: 1 },
  week: { runs: 30, failed: 2 },
  at: '2026-10-28T12:00:00Z',
}

const alices: Schemas['UserOverview'] = {
  id: 'u-1',
  email: 'alice@example.com',
  stats: {
    workflows: [
      {
        id: 'wf-1',
        name: 'Copy Alice',
        active: true,
        trading: {
          mode: 'paper',
          profit: '9.9',
          realized: '7.5',
          unrealized: '2.4',
          positions: 3,
          unpriced: 0,
          volume: '100',
          orders: 9,
          wins: 3,
          closed: 5,
        },
        runs: { day: counts, week: { runs: 30, failed: 2 } },
      },
    ],
    traders: [],
    paper: instance.users[0].paper,
    live: totals(),
    day: counts,
    week: { runs: 30, failed: 2 },
    at: '2026-10-28T12:00:00Z',
  },
}

function stats() {
  server.use(
    http.get('/api/v1/me', () => HttpResponse.json(root)),
    http.get('/api/v1/admin/stats', () => HttpResponse.json(instance)),
    http.get('/api/v1/admin/stats/users/:id', ({ params }) =>
      params.id === 'u-1'
        ? HttpResponse.json(alices)
        : HttpResponse.json(
            { error: { code: 'not_found', message: 'not found' } },
            { status: 404 },
          ),
    ),
  )
}

const rowNames = (table: HTMLElement) =>
  within(table)
    .getAllByRole('row')
    .slice(1)
    .map((r) => within(r).getAllByRole('cell')[0].textContent)

describe('admin stats (D431)', () => {
  it('is a tab of the admin pages, every user with workflows, the most profitable first', async () => {
    stats()
    renderApp('/admin/stats')

    const totals = await screen.findByRole('region', { name: 'Totals' })
    expect(screen.getByRole('link', { name: 'Stats' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: 'Paper' }), 'nothing live').toHaveAttribute(
      'aria-pressed',
      'true',
    )

    expect(within(totals).getByText('3 workflows on paper')).toBeInTheDocument()

    const users = screen.getByRole('table', { name: 'Users' })
    expect(rowNames(users)).toEqual([
      'alice@example.com',
      'bob@example.comDisabled',
      'root@example.comAdmin',
    ])
    const alice = within(users).getByRole('row', { name: /alice@example.com/ })
    expect(within(alice).getAllByRole('cell')[1]).toHaveTextContent('2 of 4')
    expect(within(alice).getAllByRole('cell')[3]).toHaveTextContent('+$9.90')
    expect(within(users).getByRole('row', { name: /root@example.com/ })).toHaveTextContent('—')
  })

  it("opens a user's dashboard, their workflows not linked", async () => {
    stats()
    const { user, router } = renderApp('/admin/stats')

    await user.click(await screen.findByRole('link', { name: 'alice@example.com' }))
    expect(router.state.location.pathname).toBe('/admin/stats/u-1')

    expect(
      await screen.findByRole('heading', { name: "alice@example.com's workflows" }),
    ).toBeInTheDocument()
    const workflows = screen.getByRole('table', { name: 'Workflows' })
    expect(within(workflows).getByText('Copy Alice')).toBeInTheDocument()
    expect(within(workflows).queryByRole('link'), "another user's: not opened").toBeNull()
    expect(screen.getByRole('region', { name: 'Traders' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Customize' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: 'Back to stats' }))
    expect(router.state.location.pathname).toBe('/admin/stats')
  })

  it('says so for no such user', async () => {
    stats()
    renderApp('/admin/stats/u-9')

    expect(await screen.findByRole('alert')).toHaveTextContent(/not found/i)
  })
})
