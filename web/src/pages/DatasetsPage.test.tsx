import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const forecasts: Schemas['Dataset'] = {
  name: 'forecasts',
  keyField: 'city',
  keepDays: 90,
  keepRows: 0,
  rows: 1234,
  bytes: 2048,
  createdAt: '2026-11-24T12:00:00Z',
  updatedAt: '2026-11-24T12:00:00Z',
}

function serve(list: Schemas['Dataset'][] = [forecasts]) {
  const deleted: string[] = []

  server.use(
    signedIn,
    http.get('/api/v1/datasets', () => HttpResponse.json({ datasets: list })),
    http.get('/api/v1/datasets/forecasts/rows', () =>
      HttpResponse.json({
        rows: [
          { seq: 1, key: 'NYC', at: '2026-11-24T12:00:00Z', row: { city: 'NYC', high: 51 } },
          {
            seq: 2,
            key: 'LDN',
            at: '2026-11-24T12:00:00Z',
            row: { city: 'LDN', models: { gfs: 9 } },
          },
        ],
      }),
    ),
    http.delete('/api/v1/datasets/:name', ({ params }) => {
      deleted.push(params.name as string)
      return new HttpResponse(null, { status: 204 })
    }),
  )

  return deleted
}

describe('datasets', () => {
  it('lists yours with their rows and size, shows the first rows, downloads and deletes', async () => {
    const deleted = serve()
    const { user } = renderApp('/data')

    const table = await screen.findByRole('table', { name: 'Datasets' })
    const row = within(table).getByRole('row', { name: /forecasts/ })
    expect(row).toHaveTextContent('city')
    expect(row).toHaveTextContent('1,234')
    expect(row).toHaveTextContent('2 KiB')
    expect(row).toHaveTextContent('90 days')
    expect(within(row).getByRole('link', { name: 'Download forecasts as CSV' })).toHaveAttribute(
      'href',
      '/api/v1/datasets/forecasts/csv',
    )

    await user.click(within(row).getByRole('button', { name: 'forecasts' }))
    const preview = await screen.findByRole('region', { name: "forecasts's first rows" })
    expect(await within(preview).findByText('NYC')).toBeInTheDocument()
    expect(within(preview).getByText('{"gfs":9}')).toBeInTheDocument()

    await user.click(within(row).getByRole('button', { name: 'Delete forecasts' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText(/1,234 rows go with it/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(deleted).toEqual(['forecasts']))
  })

  it('says how to make one, and when an admin keeps them from you', async () => {
    serve([])
    server.use(http.get('/api/v1/me', () => HttpResponse.json({ ...alice, mayUseData: false })))
    renderApp('/data/datasets')

    expect(await screen.findByText(/None yet/)).toBeInTheDocument()
    expect(screen.getByText(/Datasets are not open to you here/)).toBeInTheDocument()
  })
})
