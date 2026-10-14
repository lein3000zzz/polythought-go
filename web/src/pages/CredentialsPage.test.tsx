import '@/test/browser'

import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const header: Schemas['CredentialType'] = {
  name: 'http.header',
  label: 'HTTP header',
  description: 'A header sent with each request.',
  fields: [
    { name: 'header', label: 'Header', required: true, secret: false },
    { name: 'value', label: 'Value', required: true, secret: true },
  ],
  testable: false,
}

const apiKey: Schemas['CredentialType'] = {
  name: 'test.api',
  label: 'Test API',
  fields: [{ name: 'key', label: 'Key', required: true, secret: true }],
  testable: true,
}

function credential(overrides: Partial<Schemas['Credential']> = {}): Schemas['Credential'] {
  return {
    id: 'c-1',
    type: 'http.header',
    name: 'Relay',
    values: { header: 'Authorization' },
    secrets: ['value'],
    createdAt: '2026-10-01T10:00:00Z',
    updatedAt: '2026-10-01T10:00:00Z',
    ...overrides,
  }
}

function vault(credentials: Schemas['Credential'][], types = [header, apiKey]) {
  server.use(
    signedIn,
    http.get('/api/v1/credential-types', () => HttpResponse.json({ credentialTypes: types })),
    http.get('/api/v1/credentials', () => HttpResponse.json({ credentials })),
  )
}

const row = (name: string) => screen.getByRole('cell', { name }).closest('tr')!

describe('credentials page', () => {
  it('lists credentials without their secrets', async () => {
    vault([
      credential(),
      credential({
        id: 'c-2',
        type: 'test.api',
        name: 'Broken key',
        values: {},
        secrets: ['key'],
        testedAt: '2026-10-02T10:00:00Z',
        testError: 'the service refused the key',
      }),
    ])
    renderApp('/credentials')

    expect(await screen.findByRole('cell', { name: 'Relay' })).toBeInTheDocument()
    const relay = within(row('Relay'))
    expect(relay.getByText('HTTP header')).toBeInTheDocument()
    expect(relay.getByText('Header: Authorization')).toBeInTheDocument()
    expect(relay.getByText('Value saved')).toBeInTheDocument()
    expect(relay.queryByRole('button', { name: 'Test' })).not.toBeInTheDocument()

    const broken = within(row('Broken key'))
    expect(broken.getByText('Failed: the service refused the key')).toBeInTheDocument()
    expect(broken.getByRole('button', { name: 'Test' })).toBeInTheDocument()
  })

  it('says when there are none', async () => {
    vault([])
    renderApp('/credentials')

    expect(await screen.findByText('No credentials yet.')).toBeInTheDocument()
  })

  it('creates one, showing problems by field', async () => {
    const sent: unknown[] = []
    vault([])
    server.use(
      http.post('/api/v1/credentials', async ({ request }) => {
        const body = (await request.json()) as Schemas['CreateCredentialRequest']
        sent.push(body)

        if (body.values.header === 'X Bad') {
          return HttpResponse.json(
            {
              error: {
                code: 'invalid_input',
                message: 'invalid input',
                problems: [{ message: '"X Bad" is not a header name' }],
              },
            },
            { status: 400 },
          )
        }

        if (!body.values.value) {
          return HttpResponse.json(
            {
              error: {
                code: 'invalid_input',
                message: 'invalid input: Value is required',
                problems: [{ param: 'values.value', message: 'Value is required' }],
              },
            },
            { status: 400 },
          )
        }

        return HttpResponse.json(credential({ name: body.name }), { status: 201 })
      }),
    )
    const { user } = renderApp('/credentials')

    await user.click(await screen.findByRole('button', { name: 'New credential' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('A header sent with each request.')).toBeInTheDocument()
    expect(dialog.getByLabelText('Value')).toHaveAttribute('type', 'password')

    await user.type(dialog.getByLabelText('Name'), 'Relay')
    await user.type(dialog.getByLabelText('Header'), 'X Bad')
    await user.click(dialog.getByRole('button', { name: 'Create' }))
    expect(await dialog.findByText('"X Bad" is not a header name')).toBeInTheDocument()

    await user.clear(dialog.getByLabelText('Header'))
    await user.type(dialog.getByLabelText('Header'), 'Authorization')
    await user.click(dialog.getByRole('button', { name: 'Create' }))
    expect(await dialog.findByText('Value is required')).toBeInTheDocument()
    expect(dialog.getByLabelText('Value')).toHaveAttribute('aria-invalid', 'true')

    await user.type(dialog.getByLabelText('Value'), 'Bearer t-1')
    await user.click(dialog.getByRole('button', { name: 'Create' }))

    expect(await screen.findByText('Created Relay')).toBeInTheDocument()
    expect(sent.at(-1)).toEqual({
      type: 'http.header',
      name: 'Relay',
      values: { header: 'Authorization', value: 'Bearer t-1' },
    })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('only sends the fields of the kind chosen', async () => {
    const sent: unknown[] = []
    vault([])
    server.use(
      http.post('/api/v1/credentials', async ({ request }) => {
        sent.push(await request.json())
        return HttpResponse.json(credential(), { status: 201 })
      }),
    )
    const { user } = renderApp('/credentials')

    await user.click(await screen.findByRole('button', { name: 'New credential' }))
    const dialog = within(await screen.findByRole('dialog'))
    await user.type(dialog.getByLabelText('Header'), 'Authorization')
    await user.selectOptions(dialog.getByLabelText('Kind'), 'test.api')
    await user.type(dialog.getByLabelText('Name'), 'Key')
    await user.type(dialog.getByLabelText('Key'), 'k-1')
    await user.click(dialog.getByRole('button', { name: 'Create' }))

    await waitFor(() => expect(sent).toHaveLength(1))
    expect(sent[0]).toEqual({ type: 'test.api', name: 'Key', values: { key: 'k-1' } })
  })

  it('edits one, keeping a secret left empty', async () => {
    let sent: unknown
    vault([credential()])
    server.use(
      http.patch('/api/v1/credentials/c-1', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json(credential({ name: 'Renamed' }))
      }),
    )
    const { user } = renderApp('/credentials')

    await user.click(await screen.findByRole('button', { name: 'Edit Relay' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByLabelText('Header')).toHaveValue('Authorization')
    expect(dialog.getByLabelText('Value')).toHaveValue('')
    expect(dialog.getByLabelText('Value')).toHaveAttribute(
      'placeholder',
      'Saved. Type a new value to replace it.',
    )
    expect(dialog.queryByLabelText('Kind')).not.toBeInTheDocument()

    await user.clear(dialog.getByLabelText('Name'))
    await user.type(dialog.getByLabelText('Name'), 'Renamed')
    await user.click(dialog.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Credential saved')).toBeInTheDocument()
    expect(sent).toEqual({ name: 'Renamed', values: { header: 'Authorization', value: '' } })
  })

  it('tests and deletes', async () => {
    let deleted = false
    vault([credential({ id: 'c-2', type: 'test.api', name: 'Key', values: {}, secrets: ['key'] })])
    server.use(
      http.post('/api/v1/credentials/c-2/test', () =>
        HttpResponse.json(
          credential({
            id: 'c-2',
            type: 'test.api',
            name: 'Key',
            testedAt: '2026-10-02T10:00:00Z',
            testError: 'the service refused the key',
          }),
        ),
      ),
      http.delete('/api/v1/credentials/c-2', () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/credentials')

    await user.click(await screen.findByRole('button', { name: 'Test' }))
    expect(await screen.findByText('Key: the service refused the key')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Delete Key' }))
    const confirm = within(await screen.findByRole('alertdialog'))
    expect(confirm.getByText(/Workflows that use it fail/)).toBeInTheDocument()
    await user.click(confirm.getByRole('button', { name: 'Delete' }))

    expect(await screen.findByText('Deleted Key')).toBeInTheDocument()
    expect(deleted).toBe(true)
  })
})
