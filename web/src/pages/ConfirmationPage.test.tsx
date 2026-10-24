import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const asking: Schemas['Confirmation'] = {
  id: 'c-1',
  kind: 'run',
  workflowId: 'wf-1',
  asker: 'Claude Code',
  what: 'Run "Spread watch" by hand, which trades live',
  status: 'pending',
  createdAt: '2026-10-24T12:00:00Z',
  expiresAt: '2026-10-24T13:00:00Z',
}

describe('the page an agent links to (D409)', () => {
  it('confirms the request, and says to go back to the agent', async () => {
    let confirmed = ''
    let now = asking
    server.use(
      signedIn,
      http.get('/api/v1/me/confirmations', () => HttpResponse.json({ confirmations: [now] })),
      http.post('/api/v1/me/confirmations/:id/confirm', ({ params }) => {
        confirmed = params.id as string
        now = { ...asking, status: 'done', result: 'execution ex-1, succeeded', decidedVia: 'app' }
        return HttpResponse.json({ ...asking, status: 'confirmed' })
      }),
    )
    const { user } = renderApp('/confirmations/c-1')

    const request = within(await screen.findByRole('region', { name: 'The request' }))
    expect(request.getByText(/asks you to confirm: Run "Spread watch" by hand/)).toBeInTheDocument()
    await user.click(request.getByRole('button', { name: 'Confirm' }))

    await waitFor(() => expect(confirmed).toBe('c-1'))
    expect(await request.findByRole('status')).toHaveTextContent(
      'Confirmed, and done. execution ex-1, succeeded. You can go back to your agent.',
    )
  })

  it('shows what a change changes before you confirm it (A3)', async () => {
    const changing: Schemas['Confirmation'] = {
      ...asking,
      kind: 'change',
      what: 'Change "Spread watch", which trades live and is on',
      detail: '~ node gate (if): limit 1 → 2\n+ node buy (place_order): size 50',
    }
    server.use(
      signedIn,
      http.get('/api/v1/me/confirmations', () => HttpResponse.json({ confirmations: [changing] })),
    )
    renderApp('/confirmations/c-1')

    const request = within(await screen.findByRole('region', { name: 'The request' }))
    expect(request.getByText('What it changes')).toBeVisible()
    expect(request.getByText(/~ node gate \(if\): limit 1 → 2/)).toBeVisible()
    expect(request.getByText(/\+ node buy \(place_order\): size 50/)).toBeVisible()
  })

  it('says when there is no such request of yours', async () => {
    server.use(signedIn)
    renderApp('/confirmations/c-9')

    expect(await screen.findByText(/There is no such request of yours/)).toBeInTheDocument()
  })
})
