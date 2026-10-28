import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Workflow } from '@/api/client'
import { apiError, signedIn, workflow } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

/** The server's workflows: the pinned in their order, the rest, a search on both. */
function workflows(all: Workflow[]) {
  const seen: URLSearchParams[] = []

  const handler = http.get('/api/v1/workflows', ({ request }) => {
    const params = new URL(request.url).searchParams
    seen.push(params)

    const q = (params.get('q') ?? '').toLowerCase()
    const active = params.get('active')
    const pinned = params.get('pinned')

    let list = all.filter(
      (w) =>
        (q === '' || `${w.name} ${w.description}`.toLowerCase().includes(q)) &&
        (active === null || String(w.active) === active),
    )

    if (pinned === 'true') {
      list = list.filter((w) => w.pin).sort((a, b) => (a.pin ?? 0) - (b.pin ?? 0))
    } else if (pinned === 'false') {
      list = list.filter((w) => !w.pin)
    }

    return HttpResponse.json({ workflows: list })
  })

  return { handler, seen }
}

const spread = workflow({ description: 'Logs markets whose spread is tight', active: true })
const whales = workflow({ id: 'wf-2', name: 'Whale alert', description: 'Follows big traders' })

describe('workflows page', () => {
  it('shows workflows as cards, and loads more', async () => {
    const cursors: (string | null)[] = []

    server.use(
      signedIn,
      http.get('/api/v1/workflows', ({ request }) => {
        const params = new URL(request.url).searchParams
        if (params.get('pinned') === 'true') return HttpResponse.json({ workflows: [] })

        const cursor = params.get('cursor')
        cursors.push(cursor)

        return cursor === null
          ? HttpResponse.json({ workflows: [spread], nextCursor: 'wf-1' })
          : HttpResponse.json({ workflows: [whales] })
      }),
    )

    const { user } = renderApp('/workflows')

    const list = within(await screen.findByRole('list', { name: 'Workflows' }))
    const card = (await list.findByRole('link', { name: 'Spread watch' })).closest('li')!
    expect(card).toHaveTextContent('Logs markets whose spread is tight')
    expect(card).toHaveTextContent('Active')
    expect(card).toHaveTextContent('2 nodes')

    await user.click(screen.getByRole('button', { name: 'Load more' }))

    expect(await screen.findByRole('link', { name: 'Whale alert' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
    expect(cursors).toEqual([null, 'wf-1'])
  })

  it('says on each card what it made, and its last run (D429)', async () => {
    const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()
    const counts = { runs: 0, failed: 0 }
    const result = {
      mode: 'paper' as const,
      profit: '12.4',
      realized: '10',
      unrealized: '2.4',
      positions: 3,
      unpriced: 0,
      volume: '90',
      orders: 7,
      wins: 2,
      closed: 4,
    }
    const quiet = workflow({ id: 'wf-3', name: 'Quiet one' })

    server.use(
      signedIn,
      workflows([spread, whales, quiet]).handler,
      http.get('/api/v1/workflows/stats', () =>
        HttpResponse.json({
          workflows: [
            {
              id: spread.id,
              name: spread.name,
              active: true,
              trading: result,
              runs: {
                day: counts,
                week: counts,
                last: { id: 'ex-1', status: 'succeeded', startedAt: ago(6), finishedAt: ago(5) },
              },
            },
            {
              id: whales.id,
              name: whales.name,
              active: false,
              trading: { ...result, mode: 'live', profit: '-1.2', unpriced: 1 },
              runs: {
                day: counts,
                week: counts,
                last: { id: 'ex-2', status: 'failed', startedAt: ago(121), finishedAt: ago(120) },
              },
            },
            { id: quiet.id, name: quiet.name, active: false, runs: { day: counts, week: counts } },
          ],
          paper: { ...result, workflows: 1 },
          live: { ...result, workflows: 1 },
          day: counts,
          week: counts,
          at: ago(0),
        }),
      ),
    )

    renderApp('/workflows')

    const list = within(await screen.findByRole('list', { name: 'Workflows' }))
    const card = async (name: string) => (await list.findByRole('link', { name })).closest('li')!

    // The stats come in their own request, after the cards perhaps.
    const made = await card('Spread watch')
    await waitFor(() => expect(made).toHaveTextContent('+$12.40 paper · ran 5 min ago'))

    const lost = await card('Whale alert')
    await waitFor(() => expect(lost).toHaveTextContent('≈ −$1.20 live · failed 2 h ago'))
    expect(within(lost).getByTitle('The last run failed')).toBeInTheDocument()
    expect(within(lost).getByTitle('1 open position has no price now: at cost')).toBeInTheDocument()

    const none = await card('Quiet one')
    expect(none).toHaveTextContent('never ran')
    expect(none).not.toHaveTextContent('paper')
  })

  it('offers a retry when loading fails', async () => {
    let attempts = 0

    server.use(
      signedIn,
      http.get('/api/v1/workflows', ({ request }) => {
        attempts++
        const pinned = new URL(request.url).searchParams.get('pinned') === 'true'

        return attempts <= 2
          ? apiError(503, 'upstream_unavailable', 'upstream service unavailable')
          : HttpResponse.json({ workflows: pinned ? [] : [workflow()] })
      }),
    )

    const { user } = renderApp('/workflows')

    expect((await screen.findAllByText('Upstream service unavailable')).length).toBeGreaterThan(0)
    for (const retry of screen.getAllByRole('button', { name: 'Try again' })) {
      await user.click(retry)
    }

    expect(await screen.findByRole('link', { name: 'Spread watch' })).toBeInTheDocument()
  })

  it('searches names and descriptions, and narrows to those on or off', async () => {
    const { handler, seen } = workflows([spread, whales])
    server.use(signedIn, handler)

    const { user } = renderApp('/workflows')
    await screen.findByRole('link', { name: 'Whale alert' })

    await user.type(screen.getByRole('searchbox', { name: 'Search workflows' }), 'traders')

    await waitFor(() =>
      expect(screen.queryByRole('link', { name: 'Spread watch' })).not.toBeInTheDocument(),
    )
    expect(screen.getByRole('link', { name: 'Whale alert' })).toBeInTheDocument()
    expect(seen.some((p) => p.get('q') === 'traders')).toBe(true)

    await user.clear(screen.getByRole('searchbox', { name: 'Search workflows' }))
    await user.click(screen.getByRole('button', { name: 'Active' }))

    await waitFor(() =>
      expect(screen.queryByRole('link', { name: 'Whale alert' })).not.toBeInTheDocument(),
    )
    expect(seen.some((p) => p.get('active') === 'true')).toBe(true)

    await user.type(screen.getByRole('searchbox', { name: 'Search workflows' }), 'nothing like it')
    expect(await screen.findByText('No workflows match.')).toBeInTheDocument()
  })

  it('pins a workflow', async () => {
    let pinned: unknown

    const { handler } = workflows([spread])
    server.use(
      signedIn,
      handler,
      http.put('/api/v1/workflows/wf-1/pin', async ({ request }) => {
        pinned = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )

    const { user } = renderApp('/workflows')

    await user.click(await screen.findByRole('button', { name: 'Pin Spread watch' }))
    await waitFor(() => expect(pinned).toEqual({ pinned: true }))
  })

  it('reorders the pinned by the keyboard and by dragging', async () => {
    const orders: unknown[] = []
    const all = [
      { ...spread, pin: 1 },
      { ...whales, pin: 2 },
      workflow({ id: 'wf-3', name: 'Weather bets' }),
    ]

    const { handler } = workflows(all)
    server.use(
      signedIn,
      handler,
      http.put('/api/v1/workflows/pins', async ({ request }) => {
        const body = (await request.json()) as { ids: string[] }
        orders.push(body)
        body.ids.forEach((id, i) => {
          all.find((w) => w.id === id)!.pin = i + 1 // as the server keeps it
        })

        return new HttpResponse(null, { status: 204 })
      }),
    )

    const { user, router } = renderApp('/workflows')

    const pinnedList = await screen.findByRole('list', { name: 'Pinned workflows' })
    const names = () =>
      within(pinnedList)
        .getAllByRole('link')
        .map((l) => l.textContent)
    await waitFor(() => expect(names()).toEqual(['Spread watch', 'Whale alert']))
    expect(
      within(screen.getByRole('list', { name: 'Workflows' })).getByRole('link', {
        name: 'Weather bets',
      }),
    ).toBeInTheDocument()

    // By the keyboard: the handle and an arrow key.
    screen.getByRole('button', { name: 'Move Whale alert' }).focus()
    await user.keyboard('{ArrowLeft}')

    await waitFor(() => expect(orders).toEqual([{ ids: ['wf-2', 'wf-1'] }]))
    expect(names()).toEqual(['Whale alert', 'Spread watch'])
    expect(screen.getByText('Whale alert moved to place 1 of 2')).toBeInTheDocument()

    // By dragging: each card where it sits, Spread watch taken by its name and dropped
    // in the first place; a press that hardly moves is a click.
    const card = (name: string) => within(pinnedList).getByRole('link', { name }).closest('li')!
    const sits = (name: string, left: number) =>
      (card(name).getBoundingClientRect = () => new DOMRect(left, 0, 280, 120))
    sits('Whale alert', 0)
    sits('Spread watch', 300)

    const spreadLink = within(pinnedList).getByRole('link', { name: 'Spread watch' })
    const pointer = { pointerId: 1, button: 0, pointerType: 'mouse' }

    fireEvent.pointerDown(spreadLink, { ...pointer, clientX: 310, clientY: 50 })
    fireEvent.pointerMove(card('Spread watch'), { ...pointer, clientX: 313, clientY: 52 })
    expect(card('Whale alert').style.transform).toBe('')

    fireEvent.pointerMove(card('Spread watch'), { ...pointer, clientX: 20, clientY: 50 })
    expect(card('Whale alert').style.transform).toBe('translate(300px, 0px)')
    expect(card('Spread watch')).toHaveAttribute('data-dragging')

    fireEvent.pointerUp(card('Spread watch'), pointer)
    fireEvent.click(spreadLink)

    await waitFor(() => expect(orders).toHaveLength(2))
    expect(orders[1]).toEqual({ ids: ['wf-1', 'wf-2'] })
    await waitFor(() => expect(names()).toEqual(['Spread watch', 'Whale alert']))
    expect(card('Whale alert').style.transform).toBe('')
    expect(router.state.location.pathname).toBe('/workflows')

    // Escape puts it back; a card's pin is pressed, never dragged.
    sits('Spread watch', 0)
    sits('Whale alert', 300)
    fireEvent.pointerDown(card('Whale alert'), { ...pointer, clientX: 310, clientY: 50 })
    fireEvent.pointerMove(card('Whale alert'), { ...pointer, clientX: 20, clientY: 50 })
    expect(card('Spread watch').style.transform).toBe('translate(300px, 0px)')
    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(card('Spread watch').style.transform).toBe(''))

    const pin = within(card('Whale alert')).getByRole('button', { name: 'Unpin Whale alert' })
    fireEvent.pointerDown(pin, { ...pointer, clientX: 310, clientY: 50 })
    fireEvent.pointerMove(card('Whale alert'), { ...pointer, clientX: 20, clientY: 50 })
    expect(card('Spread watch').style.transform).toBe('')
    expect(orders).toHaveLength(2)
  })

  it('reorders none while a search hides some', async () => {
    const { handler } = workflows([
      { ...spread, pin: 1 },
      { ...whales, pin: 2 },
    ])
    server.use(signedIn, handler)

    const { user } = renderApp('/workflows')
    await screen.findByRole('button', { name: 'Move Whale alert' })

    await user.type(screen.getByRole('searchbox', { name: 'Search workflows' }), 'whale')

    expect(await screen.findByText('Clear the search to reorder them.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Move / })).not.toBeInTheDocument()
  })
})
