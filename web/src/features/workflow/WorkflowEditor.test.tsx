import '@/test/browser'

import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { FakeEventSource } from '@/test/eventSource'
import { alice, apiError, branching, nodeTypes, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

type Saved = Schemas['UpdateWorkflowRequest']

// editing serves one workflow and records what the editor saves. Opening it finds
// problems, none unless given.
function editing(workflow: Schemas['Workflow'], problems: Schemas['Problem'][] = []) {
  const saves: Saved[] = []

  server.use(
    signedIn,
    http.get('/api/v1/workflows/wf-1/problems', () => HttpResponse.json({ problems })),
    http.get('/api/v1/node-types', () => HttpResponse.json({ nodeTypes })),
    http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [] })),
    http.get('/api/v1/workflows/wf-1', () => HttpResponse.json(workflow)),
    http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [workflow] })),
    http.put('/api/v1/workflows/wf-1', async ({ request }) => {
      const body = (await request.json()) as Saved
      saves.push(body)

      return HttpResponse.json({ ...workflow, ...body, version: body.version + 1 })
    }),
  )

  return saves
}

function canvasNode(id: string): HTMLElement {
  const node = document.querySelector<HTMLElement>(`.react-flow__node[data-id="${id}"]`)
  if (!node) throw new Error(`no node ${id} on the canvas`)

  return node
}

const settings = () => within(screen.getByRole('complementary', { name: 'Settings' }))

// selectNode clicks a node on the canvas. A plain click event: user-event's mousedown
// lacks the event.view that d3-drag, which moves nodes, needs to start a drag.
async function selectNode(id: string) {
  fireEvent.click(await waitFor(() => canvasNode(id)))
}

describe('workflow editor', () => {
  it('edits a param and saves a new version', async () => {
    const saves = editing(branching())
    const { user } = renderApp('/workflows/wf-1')

    await selectNode('tight')

    const value = settings().getByLabelText(/Value/)
    expect(value).toHaveValue('0.02')
    await user.clear(value)
    await user.type(value, '0.05')
    await user.selectOptions(settings().getByLabelText('Operator'), 'lt')

    expect(screen.getByText(/Unsaved changes/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Version 4')).toBeInTheDocument()
    expect(screen.queryByText(/Unsaved changes/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()

    expect(saves).toHaveLength(1)
    expect(saves[0].version).toBe(3)
    expect(saves[0].nodes[1]).toEqual({
      id: 'tight',
      type: 'logic.if',
      params: { field: 'spread', value: '0.05', operator: 'lt' },
      position: { x: 300, y: 0 },
    })
    expect(saves[0].connections).toHaveLength(2)
  })

  it('points at the problems the server finds', async () => {
    editing(branching())
    server.use(
      http.put('/api/v1/workflows/wf-1', () =>
        HttpResponse.json(
          {
            error: {
              code: 'invalid_workflow',
              message: 'invalid workflow',
              problems: [
                {
                  nodeId: 'tight',
                  param: 'value',
                  message: 'node "tight": param "value" must be a number',
                },
                { message: 'no trigger node' },
              ],
            },
          },
          { status: 400 },
        ),
      ),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.type(await screen.findByLabelText('Workflow name'), '!')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await settings().findByText('No trigger node')).toBeInTheDocument()
    expect(within(canvasNode('tight')).getByText('1')).toBeInTheDocument()

    await selectNode('tight')
    expect(settings().getByRole('alert')).toHaveTextContent('Must be a number')

    // Editing the value clears its problem.
    await user.type(settings().getByLabelText(/Value/), '1')
    expect(settings().queryByRole('alert')).not.toBeInTheDocument()
  })

  it('marks what no longer holds when opened, without saving', async () => {
    editing(branching(), [
      { nodeId: 'tight', message: 'node "tight" (logic.if) has no output port "true"' },
    ])
    renderApp('/workflows/wf-1')

    expect(
      await screen.findByText(/This workflow no longer matches its node types/),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(canvasNode('tight').querySelector('[title]')).toHaveAttribute(
        'title',
        'node "tight" (logic.if) has no output port "true"',
      ),
    )
    expect(screen.queryByText(/Unsaved changes/)).not.toBeInTheDocument()
  })

  it('offers to reload when someone else saved first', async () => {
    let version = 3
    editing(branching())
    server.use(
      http.get('/api/v1/workflows/wf-1', () =>
        HttpResponse.json(branching({ version, name: version > 3 ? 'Changed elsewhere' : 'Mine' })),
      ),
      http.put('/api/v1/workflows/wf-1', () => {
        version = 5
        return apiError(409, 'conflict', 'changed by someone else, reload and retry')
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    const name = await screen.findByLabelText('Workflow name')
    await user.type(name, ' edited')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    const dialog = await screen.findByRole('alertdialog')
    expect(within(dialog).getByText('This workflow changed elsewhere')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Reload' }))

    await waitFor(() =>
      expect(screen.getByLabelText('Workflow name')).toHaveValue('Changed elsewhere'),
    )
    expect(screen.getByText('Version 5')).toBeInTheDocument()
  })

  it('drops error connections when a node stops continuing on failure', async () => {
    const saves = editing(
      branching({
        nodes: [
          { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
          {
            id: 'tight',
            type: 'logic.if',
            params: { field: 'spread', value: '0.02' },
            continueOnFail: true,
            position: { x: 300, y: 0 },
          },
          { id: 'log', type: 'output.log', position: { x: 600, y: 0 } },
        ],
        connections: [
          { from: 'manual', fromPort: 'main', to: 'tight', toPort: 'main' },
          { from: 'tight', fromPort: 'error', to: 'log', toPort: 'main' },
        ],
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    await selectNode('tight')
    expect(within(canvasNode('tight')).getByText('error')).toBeInTheDocument()

    await user.click(settings().getByLabelText('Continue on failure'))
    expect(within(canvasNode('tight')).queryByText('error')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByText('Version 4')

    expect(saves[0].connections).toEqual([
      { from: 'manual', fromPort: 'main', to: 'tight', toPort: 'main' },
    ])
    expect(saves[0].nodes[1].continueOnFail).toBeUndefined()
  })

  it('moves connections with a renamed button, and drops those of a removed one', async () => {
    const saves = editing(
      branching({
        nodes: [
          { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
          { id: 'ask', type: 'telegram.ask', position: { x: 300, y: 0 } },
          { id: 'log', type: 'output.log', position: { x: 600, y: 0 } },
          { id: 'log2', type: 'output.log', position: { x: 600, y: 200 } },
        ],
        connections: [
          { from: 'manual', fromPort: 'main', to: 'ask', toPort: 'main' },
          { from: 'ask', fromPort: 'Yes', to: 'log', toPort: 'main' },
          { from: 'ask', fromPort: 'No', to: 'log2', toPort: 'main' },
        ],
      }),
    )
    server.use(
      http.get('/api/v1/node-types', () =>
        HttpResponse.json({
          nodeTypes: [
            ...nodeTypes,
            {
              name: 'telegram.ask',
              label: 'Telegram question',
              trigger: false,
              inputs: ['main'],
              outputs: ['timeout'],
              params: [
                {
                  name: 'buttons',
                  label: 'Buttons',
                  kind: 'list',
                  required: false,
                  default: ['Yes', 'No'],
                },
              ],
              outputsParam: 'buttons',
            },
          ],
        }),
      ),
    )
    const { user } = renderApp('/workflows/wf-1')

    await selectNode('ask')
    const node = within(canvasNode('ask'))
    expect(node.getByText('Yes')).toBeInTheDocument()

    const buttons = settings().getByLabelText(/Buttons/)
    expect(buttons).toHaveValue('Yes\nNo')

    // Yes is typed over, letter by letter; then No goes.
    await user.type(buttons, 'Approve', { initialSelectionStart: 0, initialSelectionEnd: 3 })
    expect(node.getByText('Approve')).toBeInTheDocument()
    expect(node.queryByText('Yes')).not.toBeInTheDocument()

    await user.type(buttons, '{Backspace}', { initialSelectionStart: 7, initialSelectionEnd: 10 })
    expect(buttons).toHaveValue('Approve')
    expect(node.queryByText('No')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByText('Version 4')

    expect(saves[0].nodes[1].params).toEqual({ buttons: ['Approve'] })
    expect(saves[0].connections).toEqual([
      { from: 'manual', fromPort: 'main', to: 'ask', toPort: 'main' },
      { from: 'ask', fromPort: 'Approve', to: 'log', toPort: 'main' },
    ])
  })

  it('offers the fields of the items a node receives, and points at one they lack', async () => {
    editing(
      branching({
        nodes: [
          { id: 'markets', type: 'polymarket.feed', position: { x: 0, y: 0 } },
          {
            id: 'tight',
            type: 'logic.if',
            params: { field: 'spred', value: '0.02' },
            position: { x: 300, y: 0 },
          },
          { id: 'keep', type: 'transform.pick', position: { x: 600, y: 0 } },
        ],
        connections: [
          { from: 'markets', fromPort: 'main', to: 'tight', toPort: 'main' },
          { from: 'tight', fromPort: 'true', to: 'keep', toPort: 'main' },
        ],
      }),
    )
    const field = (name: string, kind: 'text' | 'decimal', description?: string) => ({
      name,
      kind,
      description,
      optional: false,
      nullable: false,
    })
    server.use(
      http.get('/api/v1/node-types', () =>
        HttpResponse.json({
          nodeTypes: [
            {
              name: 'polymarket.feed',
              trigger: true,
              inputs: [],
              outputs: ['main'],
              params: [],
              items: {
                passed: false,
                fields: [field('question', 'text', 'What it settles.'), field('spread', 'decimal')],
              },
            },
            {
              name: 'logic.if',
              label: 'If',
              trigger: false,
              inputs: ['main'],
              outputs: ['true', 'false'],
              items: { passed: true, fields: [] },
              params: [
                {
                  name: 'field',
                  label: 'Field',
                  kind: 'string',
                  required: true,
                  field: true,
                  fieldKinds: ['number', 'decimal'],
                },
                { name: 'value', label: 'Value', kind: 'decimal', required: true },
              ],
            },
            {
              name: 'transform.pick',
              label: 'Keep fields',
              trigger: false,
              inputs: ['main'],
              outputs: ['main'],
              items: { passed: false, picked: 'fields', fields: [] },
              params: [
                { name: 'fields', label: 'Fields', kind: 'list', required: true, field: true },
              ],
            },
          ],
        }),
      ),
    )
    const { user } = renderApp('/workflows/wf-1')

    await selectNode('tight')
    const input = settings().getByLabelText(/Field/)

    // Only numbers for If, offered as the input's suggestions.
    const options = [...document.querySelectorAll(`#${input.getAttribute('list')} option`)]
    expect(options.map((o) => o.getAttribute('value'))).toEqual(['spread'])

    expect(
      settings().getByText('The items it receives have no field "spred"; "spread"?'),
    ).toBeInTheDocument()
    await user.clear(input)
    await user.type(input, 'question')
    expect(
      settings().getByText('Field "question" is text, and this takes number or decimal'),
    ).toBeInTheDocument()
    await user.clear(input)
    await user.type(input, 'spread')
    expect(settings().queryByText(/no field|this takes/)).not.toBeInTheDocument()

    // Through If, Keep fields gets the same items, every field on offer.
    await selectNode('keep')
    await user.selectOptions(settings().getByLabelText('Add a field to Fields'), 'question')
    await user.selectOptions(settings().getByLabelText('Add a field to Fields'), 'spread')
    expect(settings().getByLabelText(/^Fields/)).toHaveValue('question\nspread')
  })

  it('asks before leaving with unsaved changes', async () => {
    editing(branching())
    const { router, user } = renderApp('/workflows/wf-1')

    await user.type(await screen.findByLabelText('Workflow name'), ' v2')
    await user.click(within(screen.getByRole('main')).getByRole('link', { name: 'Workflows' }))

    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Stay' }))
    expect(router.state.location.pathname).toBe('/workflows/wf-1')

    await user.click(within(screen.getByRole('main')).getByRole('link', { name: 'Workflows' }))
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Leave' }),
    )
    await waitFor(() => expect(router.state.location.pathname).toBe('/workflows'))
  })

  it('creates a workflow from the palette', async () => {
    let created: Schemas['WorkflowDefinition'] | undefined

    editing(branching())
    server.use(
      http.post('/api/v1/workflows', async ({ request }) => {
        created = (await request.json()) as Schemas['WorkflowDefinition']
        return HttpResponse.json(
          { ...branching(), ...created, id: 'wf-9', version: 1 },
          { status: 201 },
        )
      }),
      http.get('/api/v1/workflows/wf-9', () =>
        HttpResponse.json({ ...branching(), ...created, id: 'wf-9', version: 1 }),
      ),
    )
    const { router, user } = renderApp('/workflows/new')

    await waitFor(() => canvasNode('manual')) // every new workflow starts with a manual trigger
    expect(screen.getByText('Not saved yet')).toBeInTheDocument()

    await user.click(
      within(screen.getByRole('navigation', { name: 'Node types' })).getByText('Log'),
    )
    expect(canvasNode('log')).toBeInTheDocument()
    expect(settings().getByLabelText(/Message/)).toBeInTheDocument() // the new node is selected

    await user.type(settings().getByLabelText(/Message/), 'hello')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/workflows/wf-9'))
    expect(created?.name).toBe('Untitled workflow')
    expect(created?.nodes.map((n) => [n.id, n.params])).toEqual([
      ['manual', undefined],
      ['log', { message: 'hello' }],
    ])
  })
})

describe('running and inspecting', () => {
  const ran: Schemas['Execution'] = {
    id: 'ex-1',
    workflowVersion: 3,
    triggeredBy: 'manual',
    status: 'succeeded',
    startedAt: '2026-10-09T12:00:00Z',
    finishedAt: '2026-10-09T12:00:01.200Z',
    durationMs: 1200,
    runs: [
      {
        nodeId: 'manual',
        status: 'succeeded',
        startedAt: '2026-10-09T12:00:00Z',
        finishedAt: '2026-10-09T12:00:00Z',
        output: { main: 1 },
        samples: { main: [{ json: {} }] },
      },
      {
        nodeId: 'tight',
        status: 'succeeded',
        startedAt: '2026-10-09T12:00:00Z',
        finishedAt: '2026-10-09T12:00:01Z',
        input: { main: 1 },
        output: { true: 7 },
        samples: { true: [{ json: { market: 'Will it rain?', spread: 0.01 } }] },
      },
      {
        nodeId: 'log',
        status: 'skipped',
        startedAt: '2026-10-09T12:00:01Z',
        finishedAt: '2026-10-09T12:00:01Z',
      },
    ],
  }

  it('runs the workflow and shows what each node did', async () => {
    editing(branching())
    server.use(http.post('/api/v1/workflows/wf-1/run', () => HttpResponse.json(ran)))
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'Run' }))

    expect(await screen.findByText(/Execution at/)).toBeInTheDocument()
    expect(within(canvasNode('tight')).getByLabelText('Succeeded')).toBeInTheDocument()
    expect(within(canvasNode('log')).getByLabelText(/Skipped/)).toBeInTheDocument()
    expect(within(canvasNode('tight')).getByText('7')).toBeInTheDocument() // items on "true"

    await selectNode('tight')
    expect(settings().getByRole('tab', { name: 'Output' })).toHaveAttribute('aria-selected', 'true')
    expect(settings().getByText(/first 1 shown/)).toBeInTheDocument()
    expect(settings().getByText(/"market": "Will it rain\?"/)).toBeInTheDocument()

    await user.click(settings().getByRole('tab', { name: 'Settings' }))
    expect(settings().getByLabelText(/Value/)).toHaveValue('0.02')
  })

  it('saves unsaved changes before running', async () => {
    const calls: string[] = []
    const saves = editing(branching())
    server.use(
      http.put('/api/v1/workflows/wf-1', async ({ request }) => {
        const body = (await request.json()) as Saved
        saves.push(body)
        calls.push('save')
        return HttpResponse.json({ ...branching(), ...body, version: 4 })
      }),
      http.post('/api/v1/workflows/wf-1/run', () => {
        calls.push('run')
        return HttpResponse.json({ ...ran, workflowVersion: 4 })
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.type(await screen.findByLabelText('Workflow name'), '!')
    await user.click(screen.getByRole('button', { name: 'Save and run' }))

    await screen.findByText(/Execution at/)
    expect(calls).toEqual(['save', 'run'])
    expect(screen.queryByText(/Ran version/)).not.toBeInTheDocument()
  })

  it('shows past executions, with counts only for automatic ones', async () => {
    const scheduled: Schemas['Execution'] = {
      ...ran,
      id: 'ex-0',
      triggeredBy: 'every',
      workflowVersion: 2,
      runs: ran.runs.map((run) => ({ ...run, samples: undefined })),
    }

    editing(branching())
    server.use(
      http.get('/api/v1/workflows/wf-1/executions', () =>
        HttpResponse.json({ executions: [scheduled] }),
      ),
      http.get('/api/v1/workflows/wf-1/executions/ex-0', () => HttpResponse.json(scheduled)),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'Executions' }))
    const list = within(await screen.findByRole('region', { name: 'Executions' }))
    await user.click(await list.findByText('every'))

    expect(await screen.findByText(/Ran version 2/)).toBeInTheDocument()

    await selectNode('tight')
    expect(settings().getByText(/kept for runs started with Run only/)).toBeInTheDocument()
    expect(settings().getByText(/true · 7 items/)).toBeInTheDocument()
  })
})

describe('wallet params', () => {
  it('offer the wallets the user linked', async () => {
    let created: Schemas['WorkflowDefinition'] | undefined

    editing(branching())
    server.use(
      http.get('/api/v1/wallets', () =>
        HttpResponse.json({
          wallets: [
            {
              id: 'w-1',
              label: 'Demo',
              address: '0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed',
              hasCredentials: true,
              createdAt: '2026-10-01T10:00:00Z',
            },
          ],
        }),
      ),
      http.post('/api/v1/workflows', async ({ request }) => {
        created = (await request.json()) as Schemas['WorkflowDefinition']
        return HttpResponse.json({ ...branching(), ...created, id: 'wf-9' }, { status: 201 })
      }),
      http.get('/api/v1/workflows/wf-9', () =>
        HttpResponse.json({ ...branching(), ...created, id: 'wf-9' }),
      ),
    )
    const { user } = renderApp('/workflows/new')

    await waitFor(() => canvasNode('manual'))
    await user.click(
      within(screen.getByRole('navigation', { name: 'Node types' })).getByText('Balance'),
    )

    const wallet = settings().getByLabelText(/Wallet/)
    await waitFor(() => expect(wallet).toBeEnabled())
    expect(within(wallet).getByRole('option', { name: 'Demo · 0x5aAe…eAed' })).toBeInTheDocument()

    await user.selectOptions(wallet, 'w-1')
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(created).toBeDefined())
    expect(created?.nodes[1]).toMatchObject({ id: 'balance', params: { wallet: 'w-1' } })
  })
})

describe('following live runs', () => {
  const run = (id: string, startedAt: string, status: Schemas['Execution']['status']) => ({
    id,
    workflowVersion: 3,
    triggeredBy: 'every',
    status,
    startedAt,
    finishedAt: startedAt,
    durationMs: 5,
    error: status === 'failed' ? 'node "tight": boom' : undefined,
    runs: [
      {
        nodeId: 'tight',
        status: status === 'failed' ? ('failed' as const) : ('succeeded' as const),
        startedAt,
        finishedAt: startedAt,
        output: { true: status === 'failed' ? 0 : 4 },
        error: status === 'failed' ? 'node "tight": boom' : undefined,
      },
    ],
  })

  it('shows each new execution as it finishes', async () => {
    const first = run('ex-1', '2026-10-09T12:00:00Z', 'succeeded')

    editing(branching())
    server.use(
      http.get('/api/v1/workflows/wf-1/executions', () =>
        HttpResponse.json({ executions: [first] }),
      ),
      http.get('/api/v1/workflows/wf-1/executions/ex-1', () => HttpResponse.json(first)),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'Executions' }))
    await user.click(await screen.findByLabelText('Show new runs as they finish'))

    await waitFor(() =>
      expect(within(canvasNode('tight')).getByLabelText('Succeeded')).toBeInTheDocument(),
    )

    act(() =>
      FakeEventSource.open[0].emit('execution', {
        workflowId: 'wf-1',
        execution: run('ex-2', '2026-10-09T12:01:00Z', 'failed'),
      }),
    )

    expect(await within(canvasNode('tight')).findByLabelText('Failed')).toBeInTheDocument()
    expect(screen.getAllByText('node "tight": boom')).toHaveLength(2) // the bar and the list
    expect(
      within(screen.getByRole('region', { name: 'Executions' })).getAllByRole('listitem'),
    ).toHaveLength(2)

    // Picking an execution stops following.
    await user.click(
      within(screen.getByRole('region', { name: 'Executions' })).getAllByRole('button')[2],
    )
    expect(screen.getByLabelText('Show new runs as they finish')).not.toBeChecked()
  })
})

describe('notes and sharing', () => {
  it('saves notes with the node and shows them on the canvas', async () => {
    const saves = editing(branching())
    const { user } = renderApp('/workflows/wf-1')

    await selectNode('tight')
    await user.type(settings().getByLabelText('Notes'), 'Two cents is tight')
    expect(within(canvasNode('tight')).getByText('Two cents is tight')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByText('Version 4')
    expect(saves[0].nodes[1].notes).toBe('Two cents is tight')
  })

  it('shares the saved workflow to the library', async () => {
    let shared: unknown
    editing(branching())
    server.use(
      http.get('/api/v1/workflows/wf-1/share', () => apiError(404, 'not_found', 'not found')),
      http.put('/api/v1/workflows/wf-1/share', async ({ request }) => {
        shared = await request.json()
        return HttpResponse.json({
          id: 'tpl-1',
          name: 'Spread watch',
          summary: 'Short',
          description: 'Long',
          builtIn: false,
          mine: true,
          nodeTypes: [],
          inputs: [],
          updatedAt: '2026-10-09T12:00:00Z',
        })
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'More actions' }))
    await user.click(await screen.findByRole('menuitem', { name: /Share to library/ }))

    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('Share to the library')).toBeInTheDocument()
    expect(dialog.getByText(/Wallets and webhook URLs are left out/)).toBeInTheDocument()

    await user.type(dialog.getByLabelText('Summary'), 'Short')
    await user.type(dialog.getByLabelText('Description'), 'Long')
    await user.click(dialog.getByRole('button', { name: 'Share' }))

    expect(await screen.findByText('Shared to the library')).toBeInTheDocument()
    expect(shared).toEqual({ summary: 'Short', description: 'Long' })
  })

  it('updates or removes a copy shared before, warning about unsaved changes', async () => {
    let removed = false
    editing(branching())
    server.use(
      http.get('/api/v1/workflows/wf-1/share', () =>
        HttpResponse.json({
          id: 'tpl-1',
          name: 'Spread watch',
          summary: 'Was shared',
          description: 'Before',
          builtIn: false,
          mine: true,
          nodeTypes: [],
          inputs: [],
          updatedAt: '2026-10-09T12:00:00Z',
        }),
      ),
      http.delete('/api/v1/library/tpl-1', () => {
        removed = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.type(await screen.findByLabelText('Workflow name'), '!') // unsaved change
    await user.click(screen.getByRole('button', { name: 'More actions' }))
    await user.click(await screen.findByRole('menuitem', { name: /Share to library/ }))

    const dialog = within(await screen.findByRole('dialog'))
    expect(await dialog.findByText('Update the shared copy')).toBeInTheDocument()
    expect(dialog.getByLabelText('Summary')).toHaveValue('Was shared')
    expect(dialog.getByText(/shares the last saved version/)).toBeInTheDocument()

    await user.click(dialog.getByRole('button', { name: 'Remove from library' }))
    expect(await screen.findByText('Removed from the library')).toBeInTheDocument()
    expect(removed).toBe(true)
  })
})

describe('node list', () => {
  it('closes to make room for the canvas, and stays closed', async () => {
    editing(branching())
    const first = renderApp('/workflows/wf-1')

    const nodeTypesNav = await screen.findByRole('navigation', { name: 'Node types' })
    expect(nodeTypesNav).toBeInTheDocument()

    await first.user.click(screen.getByRole('button', { name: 'Hide node list' }))
    expect(screen.queryByRole('navigation', { name: 'Node types' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Show node list' })).toHaveAttribute(
      'aria-expanded',
      'false',
    )

    // Remembered for the next visit.
    first.router.dispose()
    cleanup()
    const second = renderApp('/workflows/wf-1')
    await screen.findByRole('button', { name: 'Show node list' })
    expect(screen.queryByRole('navigation', { name: 'Node types' })).not.toBeInTheDocument()

    await second.user.click(screen.getByRole('button', { name: 'Show node list' }))
    expect(screen.getByRole('navigation', { name: 'Node types' })).toBeInTheDocument()
  })

  it('searches the nodes, and adds the first match on Enter', async () => {
    editing(branching())
    const { user } = renderApp('/workflows/wf-1')

    const search = await screen.findByRole('searchbox', { name: 'Search nodes' })
    const nav = () => within(screen.getByRole('navigation', { name: 'Node types' }))

    // Every word must match, in the name, the type, the description or the group.
    await user.type(search, 'numeric value')
    expect(nav().getByText('If')).toBeInTheDocument()
    expect(nav().queryByText('Log')).not.toBeInTheDocument()

    await user.clear(search)
    await user.type(search, 'polymarket')
    expect(nav().getByText('Balance')).toBeInTheDocument()
    expect(nav().getAllByRole('button')).toHaveLength(1)

    await user.keyboard('{Enter}')
    expect(canvasNode('balance')).toBeInTheDocument()
    expect(search).toHaveValue('')

    await user.type(search, 'nothing like this')
    expect(screen.getByText('No nodes match “nothing like this”.')).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Node types' })).not.toBeInTheDocument()

    await user.keyboard('{Escape}')
    expect(search).toHaveValue('')
    expect(nav().getByText('Log')).toBeInTheDocument()
  })

  describe('code nodes', () => {
    const filter: Schemas['NodeType'] = {
      name: 'code.filter',
      label: 'Code: filter',
      description: 'Keeps the items TypeScript says to keep.',
      trigger: false,
      code: true,
      inputs: ['main'],
      outputs: ['main'],
      params: [],
    }

    function withCode(mayWriteCode: boolean) {
      const flow = branching()
      editing({
        ...flow,
        nodes: [...flow.nodes, { id: 'keep', type: 'code.filter', position: { x: 900, y: 0 } }],
      })
      server.use(
        http.get('/api/v1/me', () => HttpResponse.json({ ...alice, mayWriteCode })),
        http.get('/api/v1/node-types', () =>
          HttpResponse.json({ nodeTypes: [...nodeTypes, filter] }),
        ),
      )
    }

    const nodesOnCanvas = () => document.querySelectorAll('.react-flow__node').length

    it('are shown but not added when an admin turned code off', async () => {
      withCode(false)
      const { user } = renderApp('/workflows/wf-1')

      const nav = within(await screen.findByRole('navigation', { name: 'Node types' }))
      expect(nav.getByText('An admin has turned code off for you.')).toBeInTheDocument()
      expect(nav.getByRole('button', { name: /Code: filter/ })).toBeDisabled()
      expect(nav.getByRole('button', { name: /Log/ })).toBeEnabled()
      expect(nav.queryByRole('link', { name: /New custom node/ })).not.toBeInTheDocument()

      // The one already there says so.
      await waitFor(() => canvasNode('keep'))
      expect(within(canvasNode('keep')).getByText('Code is off for you')).toBeInTheDocument()
      expect(within(canvasNode('tight')).queryByText('Code is off for you')).not.toBeInTheDocument()

      // Enter skips it.
      const before = nodesOnCanvas()
      await user.type(screen.getByRole('searchbox', { name: 'Search nodes' }), 'typescript')
      await user.keyboard('{Enter}')
      expect(nodesOnCanvas()).toBe(before)
    })

    it('are added for users who may write code', async () => {
      withCode(true)
      const { user } = renderApp('/workflows/wf-1')

      const nav = within(await screen.findByRole('navigation', { name: 'Node types' }))
      expect(nav.queryByText('An admin has turned code off for you.')).not.toBeInTheDocument()
      expect(nav.getByRole('link', { name: /New custom node/ })).toHaveAttribute(
        'href',
        '/nodes/new',
      )

      await waitFor(() => canvasNode('keep'))
      expect(within(canvasNode('keep')).queryByText('Code is off for you')).not.toBeInTheDocument()

      const before = nodesOnCanvas()
      await user.click(nav.getByRole('button', { name: /Code: filter/ }))
      expect(nodesOnCanvas()).toBe(before + 1)
    })
  })

  it('opens and focuses the search on /', async () => {
    editing(branching())
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'Hide node list' }))
    await user.click(document.body)
    await user.keyboard('/')

    const search = await screen.findByRole('searchbox', { name: 'Search nodes' })
    await waitFor(() => expect(search).toHaveFocus())
    expect(screen.getByRole('button', { name: 'Hide node list' })).toBeInTheDocument()

    // Typing / in a field is just a slash.
    await user.type(search, 'a/b')
    expect(search).toHaveValue('a/b')
  })
})

describe('waiting executions', () => {
  const waiting: Schemas['Execution'] = {
    id: 'ex-9',
    workflowVersion: 3,
    triggeredBy: 'manual',
    status: 'waiting',
    startedAt: '2026-10-09T12:00:00Z',
    durationMs: 0,
    waitingOn: 'tight',
    waitingUntil: '2026-10-09T13:00:00Z',
    runs: [{ nodeId: 'tight', status: 'waiting', startedAt: '2026-10-09T12:00:00Z' }],
  }

  it('says where it waits, and cancels it', async () => {
    const done: Schemas['Execution'] = {
      ...waiting,
      status: 'cancelled',
      finishedAt: '2026-10-09T12:10:00Z',
      error: 'cancelled by you',
      waitingOn: undefined,
      waitingUntil: undefined,
      runs: [{ ...waiting.runs[0], status: 'cancelled' }],
    }
    let cancelled = false

    editing(branching())
    server.use(
      http.get('/api/v1/workflows/wf-1/executions', () =>
        HttpResponse.json({ executions: [cancelled ? done : waiting] }),
      ),
      http.get('/api/v1/workflows/wf-1/executions/ex-9', () =>
        HttpResponse.json(cancelled ? done : waiting),
      ),
      http.post('/api/v1/workflows/wf-1/executions/ex-9/cancel', () => {
        cancelled = true
        return HttpResponse.json(done)
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'Executions' }))
    const list = within(await screen.findByRole('region', { name: 'Executions' }))
    expect(await list.findByText(/^till /)).toBeInTheDocument()
    await user.click(list.getAllByRole('button')[1])

    expect(await screen.findByText(/Waiting at tight until/)).toBeInTheDocument()
    expect(within(canvasNode('tight')).getByLabelText('Waiting')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Cancel execution' }))

    expect(await screen.findByText('Execution cancelled')).toBeInTheDocument()
    expect(cancelled).toBe(true)
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Cancel execution' })).not.toBeInTheDocument(),
    )
    expect(
      within(canvasNode('tight')).getByLabelText('Cancelled while it waited'),
    ).toBeInTheDocument()
  })
})
