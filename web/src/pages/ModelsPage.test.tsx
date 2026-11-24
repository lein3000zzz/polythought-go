import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const limits: Schemas['MLModelLimits'] = {
  models: 20,
  workers: 10,
  items: 1000,
  requestBytes: 1048576,
  answerBytes: 1048576,
  maxWaitSeconds: 300,
  pollSeconds: 50,
}

const temps: Schemas['MLModel'] = {
  name: 'temps',
  kind: 'worker',
  workers: [
    {
      name: 'gpu-1',
      version: '1.2.0',
      since: '2026-11-24T12:00:00Z',
      seen: '2026-11-24T12:00:00Z',
      holding: 0,
    },
  ],
  lastAnswerAt: '2026-11-24T12:00:00Z',
  lastError: 'no answer within 30s',
  lastErrorAt: '2026-11-24T11:00:00Z',
  createdAt: '2026-11-24T10:00:00Z',
  updatedAt: '2026-11-24T10:00:00Z',
}

const hosted: Schemas['MLModel'] = {
  name: 'hosted',
  kind: 'url',
  url: 'https://models.example/predict',
  workers: [],
  createdAt: '2026-11-24T10:00:00Z',
  updatedAt: '2026-11-24T10:00:00Z',
}

function serve(list: Schemas['MLModel'][] = [temps, hosted]) {
  const calls = { made: [] as unknown[], keys: [] as string[], deleted: [] as string[] }

  server.use(
    signedIn,
    http.get('/api/v1/models', () => HttpResponse.json({ models: list, limits })),
    http.post('/api/v1/models', async ({ request }) => {
      const body = (await request.json()) as { name: string; kind: 'worker' | 'url' }
      calls.made.push(body)
      return HttpResponse.json(
        { ...body, workers: [], createdAt: '', updatedAt: '' },
        { status: 201 },
      )
    }),
    http.get('/api/v1/models/:name/keys', () =>
      HttpResponse.json({
        keys: [
          { id: 'k-1', name: 'laptop', prefix: 'pw_abcdefg', createdAt: '2026-11-24T10:00:00Z' },
        ],
      }),
    ),
    http.post('/api/v1/models/:name/keys', async ({ request }) => {
      const body = (await request.json()) as { name: string }
      calls.keys.push(body.name)
      return HttpResponse.json(
        {
          key: {
            id: 'k-2',
            name: body.name,
            prefix: 'pw_hijklmn',
            createdAt: '2026-11-24T12:00:00Z',
          },
          secret: 'pw_hijklmn-the-whole-secret',
        },
        { status: 201 },
      )
    }),
    http.delete('/api/v1/models/:name', ({ params }) => {
      calls.deleted.push(params.name as string)
      return new HttpResponse(null, { status: 204 })
    }),
  )

  return calls
}

describe('models', () => {
  it('lists yours with who serves them, their last answer and error', async () => {
    serve()
    renderApp('/data/models')

    const table = await screen.findByRole('table', { name: 'Models' })
    const row = within(table).getByRole('row', { name: /temps/ })
    expect(row).toHaveTextContent('1 connected: gpu-1 (1.2.0)')
    expect(row).toHaveTextContent('no answer within 30s')
    expect(within(table).getByRole('row', { name: /hosted/ })).toHaveTextContent(
      'URL: models.example',
    )
    expect(screen.getByRole('link', { name: 'Models' })).toHaveAttribute('aria-current', 'page')
  })

  it('makes a worker model, then a key whose secret shows once', async () => {
    const calls = serve([])
    const { user } = renderApp('/data/models')

    expect(await screen.findByText(/None yet/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'New model' }))

    const dialog = await screen.findByRole('dialog', { name: 'New model' })
    await user.type(within(dialog).getByLabelText('Name'), 'temps')
    await user.click(within(dialog).getByRole('button', { name: 'Make it' }))
    await waitFor(() => expect(calls.made).toEqual([{ name: 'temps', kind: 'worker' }]))

    const keys = await screen.findByRole('region', { name: "temps's worker keys" })
    expect(await within(keys).findByText('laptop')).toBeInTheDocument()

    await user.type(within(keys).getByLabelText('New key'), 'gpu-box')
    await user.click(within(keys).getByRole('button', { name: 'Make key' }))

    expect(await within(keys).findByText('pw_hijklmn-the-whole-secret')).toBeInTheDocument()
    expect(within(keys).getByText('It is shown only this once.')).toBeInTheDocument()
    expect(calls.keys).toEqual(['gpu-box'])
  })

  it('makes a URL model with its address', async () => {
    const calls = serve([])
    const { user } = renderApp('/data/models')

    await user.click(await screen.findByRole('button', { name: 'New model' }))
    const dialog = await screen.findByRole('dialog', { name: 'New model' })
    await user.type(within(dialog).getByLabelText('Name'), 'hosted')
    await user.click(within(dialog).getByRole('radio', { name: /A URL/ }))
    await user.type(within(dialog).getByLabelText('URL'), 'https://models.example/predict')
    await user.click(within(dialog).getByRole('button', { name: 'Make it' }))

    await waitFor(() =>
      expect(calls.made).toEqual([
        { name: 'hosted', kind: 'url', url: 'https://models.example/predict' },
      ]),
    )
  })

  it('deletes one, saying its workers are refused', async () => {
    const calls = serve()
    const { user } = renderApp('/data/models')

    const table = await screen.findByRole('table', { name: 'Models' })
    await user.click(within(table).getByRole('button', { name: 'Delete temps' }))

    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('its workers are refused from their next call')
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(calls.deleted).toEqual(['temps']))
  })

  it('says when an admin keeps models from you', async () => {
    serve([])
    server.use(http.get('/api/v1/me', () => HttpResponse.json({ ...alice, mayUseData: false })))
    renderApp('/data/models')

    expect(await screen.findByText(/Models are not open to you here/)).toBeInTheDocument()
  })
})
