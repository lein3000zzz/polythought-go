import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { Schemas } from '@/api/client'
import { createQueryClient } from '@/api/queryClient'
import { server } from '@/test/server'

import { HistoryPanel } from './HistoryPanel'

type Switch = Schemas['WorkflowSwitch']

const newest: Switch[] = [
  {
    id: 'sw-4',
    kind: 'off',
    polythought: true,
    reason: 'the plugin weather is disabled',
    at: '2026-10-29T12:00:00Z',
  },
  {
    id: 'sw-3',
    kind: 'live',
    by: { kind: 'agent', id: 'g-1', name: 'Claude Code', confirmed: true },
    polythought: false,
    reason: 'the user asked',
    wallet: { id: 'w-1', address: '0x5f3C00000000000000000000000000000000A1b2' },
    at: '2026-10-29T11:00:00Z',
  },
  { id: 'sw-2', kind: 'kill_on', polythought: false, at: '2026-10-29T10:00:00Z' },
]
const older: Switch[] = [
  {
    id: 'sw-1',
    kind: 'wallet',
    by: { kind: 'token', id: 't-1', name: 'cron' },
    polythought: false,
    wallet: { id: 'w-9', address: '0x00000000000000000000000000000000000000b2' },
    at: '2026-10-28T10:00:00Z',
  },
]

// history serves wf-1's history a page at a time, keeping what each request asked.
function history() {
  const asked: URLSearchParams[] = []

  server.use(
    http.get('/api/v1/wallets', () =>
      HttpResponse.json({
        wallets: [
          { id: 'w-1', label: 'Main', address: '0x5f3C00000000000000000000000000000000A1b2' },
        ],
      }),
    ),
    http.get('/api/v1/workflows/wf-1/executions', () => HttpResponse.json({ executions: [] })),
    http.get('/api/v1/workflows/wf-1/history', ({ request }) => {
      const q = new URL(request.url).searchParams
      asked.push(q)

      return HttpResponse.json(
        q.get('cursor') === 'sw-2' ? { switches: older } : { switches: newest, nextCursor: 'sw-2' },
      )
    }),
  )

  return asked
}

function renderPanel() {
  const queryClient = createQueryClient()
  queryClient.setDefaultOptions({ queries: { retry: false } })

  const view = (
    <QueryClientProvider client={queryClient}>
      <HistoryPanel
        workflowId="wf-1"
        onSelect={vi.fn()}
        follow={false}
        onFollow={vi.fn()}
        onClose={vi.fn()}
      />
    </QueryClientProvider>
  )
  const { rerender, unmount } = render(view)

  return { user: userEvent.setup(), rerender: () => rerender(view), unmount }
}

const rows = () =>
  within(screen.getByRole('tabpanel', { name: 'History' }))
    .getAllByRole('listitem')
    .map((r) => r.textContent)

describe('history panel (D433)', () => {
  beforeEach(() => localStorage.clear())

  it('opens on the executions, and says who switched it and why', async () => {
    history()
    const { user } = renderPanel()

    expect(screen.getByRole('tab', { name: 'Executions' })).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByText(/No executions yet/)).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: 'History' }))
    await screen.findByText('Switched off')

    await waitFor(() =>
      expect(rows()).toEqual([
        expect.stringMatching(/Switched off\s*by Polythought: the plugin weather is disabled$/),
        expect.stringMatching(
          /Live, from Main\s*by Claude Code, confirmed by you: “the user asked”$/,
        ),
        expect.stringMatching(/Kill switch on\s*by you$/),
      ]),
    )
    expect(screen.queryByLabelText('Show new runs as they finish')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Show older' }))
    expect(await screen.findByText('Live from another wallet, 0x0000…00b2')).toBeInTheDocument()
    expect(screen.getByText(/by the API token “cron”/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Show older' })).not.toBeInTheDocument()
  })

  it('shows the kinds ticked alone, kept for the next visit', async () => {
    const asked = history()
    const first = renderPanel()

    await first.user.click(screen.getByRole('tab', { name: 'History' }))
    await screen.findByText('Switched off')
    expect(asked[0].get('kinds'), 'every kind: none asked').toBeNull()

    const show = screen.getByRole('group', { name: 'Show' })
    await first.user.click(within(show).getByRole('button', { name: 'Kill switch' }))
    await first.user.click(within(show).getByRole('button', { name: 'Paper resets' }))
    expect(within(show).getByRole('button', { name: 'Kill switch' })).toHaveAttribute(
      'aria-pressed',
      'false',
    )
    await waitFor(() => expect(asked.at(-1)?.get('kinds')).toBe('on_off,trading'))

    first.unmount()
    const again = renderPanel()
    await again.user.click(screen.getByRole('tab', { name: 'History' }))
    expect(
      within(screen.getByRole('group', { name: 'Show' })).getByRole('button', {
        name: 'Paper resets',
      }),
    ).toHaveAttribute('aria-pressed', 'false')

    for (const kind of ['On and off', 'Paper and live']) {
      await again.user.click(
        within(screen.getByRole('group', { name: 'Show' })).getByRole('button', { name: kind }),
      )
    }
    expect(screen.getByText('Choose what to show, above.')).toBeInTheDocument()
  })
})
