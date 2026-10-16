import '@/test/browser'

import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

type CustomNode = Schemas['CustomNode']

function split(overrides: Partial<CustomNode> = {}): CustomNode {
  return {
    id: 'n-1',
    type: 'custom.spread-split',
    version: 2,
    createdAt: '2026-10-16T12:00:00Z',
    updatedAt: '2026-10-16T12:00:00Z',
    name: 'spread-split',
    label: 'Spread split',
    settings: [{ name: 'threshold', label: 'threshold', kind: 'decimal', default: '0.02' }],
    outputs: ['wide', 'tight'],
    items: { passed: true, fields: [] },
    source: 'export function run(items) { return { wide: items } }',
    ...overrides,
  }
}

const breaking = {
  error: {
    code: 'breaks_workflows',
    message: 'the change breaks workflows that use the node: "Spread watch"',
    workflows: [
      {
        id: 'wf-1',
        name: 'Spread watch',
        problems: [
          {
            nodeId: 'split',
            message: 'node "split" (custom.spread-split) has no output port "wide"',
          },
        ],
      },
    ],
  },
}

function nodes(list: CustomNode[]) {
  server.use(
    signedIn,
    http.get('/api/v1/custom-nodes', () => HttpResponse.json({ customNodes: list })),
    http.get('/api/v1/custom-nodes/:id', ({ params }) => {
      const n = list.find((c) => c.id === params.id)
      return n
        ? HttpResponse.json(n)
        : HttpResponse.json({ error: { code: 'not_found', message: 'not found' } }, { status: 404 })
    }),
  )
}

const code = () => screen.getByLabelText('Code') as HTMLTextAreaElement

describe('nodes page', () => {
  it('lists custom nodes, and deletes one after naming the workflows it breaks', async () => {
    const deletes: string[] = []
    nodes([split()])
    server.use(
      http.delete('/api/v1/custom-nodes/n-1', ({ request }) => {
        const confirm = new URL(request.url).searchParams.get('confirm')
        deletes.push(confirm ?? 'no')

        return confirm === 'true'
          ? new HttpResponse(null, { status: 204 })
          : HttpResponse.json(breaking, { status: 409 })
      }),
    )
    const { user } = renderApp('/nodes')

    const row = (await screen.findByRole('link', { name: 'Spread split' })).closest('tr')!
    expect(within(row).getByText('custom.spread-split')).toBeInTheDocument()
    expect(within(row).getByText('wide, tight')).toBeInTheDocument()

    await user.click(within(row).getByRole('button', { name: 'Delete' }))
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Delete' }),
    )

    const warning = within(
      await screen.findByRole('alertdialog', { name: 'This breaks a workflow' }),
    )
    expect(warning.getByRole('link', { name: /Spread watch/ })).toHaveAttribute(
      'href',
      '/workflows/wf-1',
    )
    expect(warning.getByText(/has no output port "wide"/)).toBeInTheDocument()

    await user.click(warning.getByRole('button', { name: 'Delete anyway' }))
    expect(await screen.findByText('Deleted custom.spread-split')).toBeInTheDocument()
    expect(deletes).toEqual(['no', 'true'])
  })

  it('makes a node from the form, its code following the form', async () => {
    const sent: unknown[] = []
    nodes([])
    server.use(
      http.post('/api/v1/custom-nodes', async ({ request }) => {
        const body = await request.json()
        sent.push(body)

        if (sent.length === 1) {
          return HttpResponse.json(
            {
              error: {
                code: 'invalid_input',
                message: 'invalid input',
                problems: [
                  { param: 'name', message: 'a name is lowercase letters, digits and hyphens' },
                  { param: 'settings.0.default', message: 'the default must be a number' },
                ],
              },
            },
            { status: 400 },
          )
        }

        return HttpResponse.json(split({ id: 'n-7' }), { status: 201 })
      }),
      http.get('/api/v1/custom-nodes/n-7', () => HttpResponse.json(split({ id: 'n-7' }))),
    )
    const { user, router } = renderApp('/nodes/new')

    expect(await screen.findByRole('heading', { name: 'New custom node' })).toBeInTheDocument()
    expect(code().value).toContain('return items')
    expect(screen.getByText(/it follows the form/)).toBeInTheDocument()

    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Spread-Split')
    await user.type(screen.getByRole('textbox', { name: 'Label' }), 'Spread split')

    await user.click(screen.getByRole('button', { name: 'Add a setting' }))
    await user.type(screen.getByLabelText('Setting 1 name'), 'threshold')
    await user.selectOptions(screen.getByLabelText('Setting 1 kind'), 'decimal')
    await user.type(screen.getByLabelText('Default'), '0.02')

    await user.clear(screen.getByLabelText('Output 1'))
    await user.type(screen.getByLabelText('Output 1'), 'wide')
    await user.click(screen.getByRole('button', { name: 'Add an output' }))
    await user.type(screen.getByLabelText('Output 2'), 'tight')

    expect(code().value).toContain('type Settings = {\n  threshold: string\n}')
    expect(code().value).toContain('wide: items,\n    tight: [],')

    await user.click(screen.getByRole('button', { name: 'Make the node' }))
    expect(
      await screen.findByText('a name is lowercase letters, digits and hyphens'),
    ).toBeInTheDocument()
    expect(screen.getByText('the default must be a number')).toBeInTheDocument()

    await user.clear(screen.getByRole('textbox', { name: 'Name' }))
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'spread-split')
    const made = code().value
    await user.click(screen.getByRole('button', { name: 'Make the node' }))

    expect(await screen.findByText('Made custom.spread-split')).toBeInTheDocument()
    await waitFor(() => expect(router.state.location.pathname).toBe('/nodes/n-7'))

    expect(sent[1]).toEqual({
      name: 'spread-split',
      label: 'Spread split',
      description: '',
      settings: [{ name: 'threshold', kind: 'decimal', default: '0.02' }],
      outputs: ['wide', 'tight'],
      items: { passed: true, fields: [] },
      source: made,
    })
  })

  it('leaves edited code alone, and declares nested item fields', async () => {
    nodes([])
    const { user } = renderApp('/nodes/new')

    await screen.findByRole('heading', { name: 'New custom node' })
    fireEvent.change(code(), { target: { value: 'export function run(items) { return items }' } })
    expect(screen.queryByText(/it follows the form/)).not.toBeInTheDocument()

    await user.click(screen.getByRole('radio', { name: /Makes items of its own/ }))
    await user.click(screen.getByRole('button', { name: 'Add a field' }))
    await user.type(screen.getByLabelText('Field name'), 'best')
    await user.selectOptions(screen.getByLabelText('Kind of best'), 'object')
    await user.type(screen.getByLabelText('Field best.name'), 'price')
    await user.selectOptions(screen.getByLabelText('Kind of best.price'), 'decimal')

    expect(code().value).toBe('export function run(items) { return items }')
    expect(screen.getByRole('button', { name: 'Add a field to best' })).toBeInTheDocument()
  })

  it('asks before saving a change that breaks workflows', async () => {
    const sent: { confirm?: boolean; outputs: string[] }[] = []
    nodes([split()])
    server.use(
      http.put('/api/v1/custom-nodes/n-1', async ({ request }) => {
        const body = (await request.json()) as { confirm?: boolean; outputs: string[] }
        sent.push(body)

        return body.confirm
          ? HttpResponse.json(split({ outputs: body.outputs, version: 3 }))
          : HttpResponse.json(breaking, { status: 409 })
      }),
    )
    const { user } = renderApp('/nodes/n-1')

    expect(await screen.findByRole('heading', { name: 'Spread split' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveAttribute('readonly')
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: 'Remove output 1' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))

    const warning = within(
      await screen.findByRole('alertdialog', { name: 'This breaks a workflow' }),
    )
    await user.click(warning.getByRole('button', { name: 'Save anyway' }))

    expect(await screen.findByText('Saved')).toBeInTheDocument()
    expect(sent.map((b) => [b.outputs, b.confirm])).toEqual([
      [['tight'], false],
      [['tight'], true],
    ])
  })

  it('shows, but does not change, nodes when code is off', async () => {
    nodes([split()])
    server.use(http.get('/api/v1/me', () => HttpResponse.json({ ...alice, mayWriteCode: false })))
    renderApp('/nodes/n-1')

    expect(await screen.findByText(/An admin has turned code off for you/)).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Label' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Delete' })).toBeEnabled()
  })
})
