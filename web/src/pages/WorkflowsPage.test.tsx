import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { apiError, signedIn, workflow } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

describe('workflows page', () => {
  it('lists workflows and loads more pages', async () => {
    const cursors: (string | null)[] = []

    server.use(
      signedIn,
      http.get('/api/v1/workflows', ({ request }) => {
        const cursor = new URL(request.url).searchParams.get('cursor')
        cursors.push(cursor)

        return cursor === null
          ? HttpResponse.json({
              workflows: [workflow({ active: true, version: 3 })],
              nextCursor: 'wf-1',
            })
          : HttpResponse.json({ workflows: [workflow({ id: 'wf-2', name: 'Whale alert' })] })
      }),
    )

    const { user } = renderApp('/workflows')

    const row = (await screen.findByRole('link', { name: 'Spread watch' })).closest('tr')!
    expect(within(row).getByText('Active')).toBeInTheDocument()
    expect(within(row).getByText('3')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Load more' }))

    expect(await screen.findByRole('link', { name: 'Whale alert' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Load more' })).not.toBeInTheDocument()
    expect(cursors).toEqual([null, 'wf-1'])
  })

  it('offers a retry when loading fails', async () => {
    let attempts = 0

    server.use(
      signedIn,
      http.get('/api/v1/workflows', () => {
        attempts++
        return attempts === 1
          ? apiError(503, 'upstream_unavailable', 'upstream service unavailable')
          : HttpResponse.json({ workflows: [workflow()] })
      }),
    )

    const { user } = renderApp('/workflows')

    expect(await screen.findByText('Upstream service unavailable')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Try again' }))

    expect(await screen.findByRole('link', { name: 'Spread watch' })).toBeInTheDocument()
  })
})
