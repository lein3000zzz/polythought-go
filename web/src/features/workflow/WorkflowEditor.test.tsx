import '@/test/browser'

import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { FakeEventSource } from '@/test/eventSource'
import { alice, apiError, branching, nodeTypes, signedIn, workflow } from '@/test/fixtures'
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
  it('saves its description with it', async () => {
    const saves = editing(branching())
    const { user } = renderApp('/workflows/wf-1')

    const description = await screen.findByRole('textbox', { name: 'Description' })
    await user.type(description, 'Logs tight markets')

    expect(screen.getByText(/Unsaved changes/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(saves).toHaveLength(1))
    expect(saves[0].description).toBe('Logs tight markets')
  })

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

  it('asks, switching off, whether to cancel its resting live orders (D217)', async () => {
    editing({ ...branching(), active: true })
    let asked: string | null = ''

    server.use(
      http.get('/api/v1/workflows/wf-1/trading/setup', () =>
        HttpResponse.json({ mode: 'live', holdsLive: false, resting: 2, liveAvailable: true }),
      ),
      http.post('/api/v1/workflows/wf-1/deactivate', ({ request }) => {
        asked = new URL(request.url).searchParams.get('cancelOrders')
        return HttpResponse.json({ ...branching(), active: false })
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    await waitFor(() => expect(screen.getByRole('switch', { name: 'Active' })).toBeChecked())
    await user.click(screen.getByRole('switch', { name: 'Active' }))

    const dialog = within(await screen.findByRole('alertdialog'))
    expect(dialog.getByText(/Its 2 live orders rest on the book/)).toBeInTheDocument()

    const cancel = dialog.getByRole('checkbox', { name: 'Also cancel its 2 resting live orders' })
    expect(cancel).not.toBeChecked()
    await user.click(cancel)
    await user.click(dialog.getByRole('button', { name: 'Switch off' }))

    await waitFor(() => expect(asked).toBe('true'))
    expect(await screen.findByText('Workflow deactivated')).toBeInTheDocument()
  })

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
      by: { kind: 'agent', id: 'g-1', name: 'Claude Code', how: 'switched_on', confirmed: true },
    }

    editing(branching())
    server.use(
      http.get('/api/v1/workflows/wf-1/executions', () =>
        HttpResponse.json({ executions: [scheduled] }),
      ),
      http.get('/api/v1/workflows/wf-1/executions/ex-0', () => HttpResponse.json(scheduled)),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'History' }))
    const list = within(await screen.findByRole('tabpanel', { name: 'Executions' }))
    // Its workflow was switched on by an agent, so the agent is behind its runs (D407).
    expect(
      await list.findByText('switched on by Claude Code, confirmed by you'),
    ).toBeInTheDocument()
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

    await user.click(await screen.findByRole('button', { name: 'History' }))
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
      within(screen.getByRole('tabpanel', { name: 'Executions' })).getAllByRole('listitem'),
    ).toHaveLength(2)

    // Picking an execution stops following.
    await user.click(
      within(screen.getByRole('tabpanel', { name: 'Executions' })).getAllByRole('button')[1],
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

  it('says the custom nodes it uses go with it, code included', async () => {
    const flow = branching()
    editing({
      ...flow,
      nodes: [...flow.nodes, { id: 'split', type: 'custom.split', position: { x: 900, y: 0 } }],
    })
    server.use(
      http.get('/api/v1/node-types', () =>
        HttpResponse.json({
          nodeTypes: [
            ...nodeTypes,
            {
              name: 'custom.split',
              label: 'Spread split',
              trigger: false,
              code: true,
              inputs: ['main'],
              outputs: ['main'],
              params: [],
            },
          ],
        }),
      ),
      http.get('/api/v1/workflows/wf-1/share', () => apiError(404, 'not_found', 'not found')),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'More actions' }))
    await user.click(await screen.findByRole('menuitem', { name: /Share to library/ }))

    const dialog = within(await screen.findByRole('dialog'))
    expect(
      dialog.getByText(/It brings your custom node Spread split: their settings, outputs and code/),
    ).toBeInTheDocument()
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

describe('settings panel (D390)', () => {
  it('folds to a rail and stays folded, and a node double-clicked opens it', async () => {
    editing(branching())
    const first = renderApp('/workflows/wf-1')

    await screen.findByRole('separator', { name: 'Resize the settings' })
    expect(settings().getByText('Workflow')).toBeInTheDocument()

    await first.user.click(screen.getByRole('button', { name: 'Hide settings' }))
    expect(screen.getByRole('button', { name: 'Show settings' })).toHaveAttribute(
      'aria-expanded',
      'false',
    )
    expect(screen.queryByRole('separator', { name: 'Resize the settings' })).not.toBeInTheDocument()

    // Remembered for the next visit.
    first.router.dispose()
    cleanup()
    renderApp('/workflows/wf-1')
    await screen.findByRole('button', { name: 'Show settings' })

    // A click selects the node and leaves the panel folded; a double-click opens it there.
    await selectNode('tight')
    expect(screen.getByRole('button', { name: 'Show settings' })).toBeInTheDocument()

    fireEvent.doubleClick(canvasNode('tight'))
    expect(await screen.findByRole('button', { name: 'Hide settings' })).toBeInTheDocument()
    expect(settings().getByRole('heading', { name: 'If' })).toBeInTheDocument()
  })

  it('keeps the History button before the views, so nothing moves as it goes', async () => {
    editing(branching())
    renderApp('/workflows/wf-1')

    const history = await screen.findByRole('button', { name: 'History' })
    const views = screen.getByRole('tablist', { name: 'View' })
    expect(history.compareDocumentPosition(views) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
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

  it('resizes from its edge, keeps its width, and goes back to it on a double-click (D369)', async () => {
    editing(branching())
    const first = renderApp('/workflows/wf-1')

    const edge = await screen.findByRole('separator', { name: 'Resize the node list' })
    const list = screen.getByRole('complementary', { name: 'Node list' })
    expect(edge).toHaveAttribute('aria-valuenow', '240')
    expect(list).toHaveStyle({ width: '240px' })

    edge.focus()
    await first.user.keyboard('{ArrowRight}{ArrowRight}')
    expect(list).toHaveStyle({ width: '272px' })
    await first.user.keyboard('{End}')
    expect(edge).toHaveAttribute('aria-valuenow', '400')

    first.router.dispose()
    cleanup()
    const second = renderApp('/workflows/wf-1')
    const again = await screen.findByRole('separator', { name: 'Resize the node list' })
    expect(again).toHaveAttribute('aria-valuenow', '400')

    await second.user.dblClick(again)
    expect(again).toHaveAttribute('aria-valuenow', '240')

    // Folded, it has no edge to drag.
    await second.user.click(screen.getByRole('button', { name: 'Hide node list' }))
    expect(
      screen.queryByRole('separator', { name: 'Resize the node list' }),
    ).not.toBeInTheDocument()
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

    await user.click(await screen.findByRole('button', { name: 'History' }))
    const list = within(await screen.findByRole('tabpanel', { name: 'Executions' }))
    expect(await list.findByText(/^till /)).toBeInTheDocument()
    await user.click(list.getAllByRole('button')[0])

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

describe('a run waiting on a Local agent node (D413)', () => {
  const onTask: Schemas['Execution'] = {
    id: 'ex-7',
    workflowVersion: 1,
    triggeredBy: 'manual',
    status: 'waiting',
    startedAt: '2026-10-09T12:00:00Z',
    durationMs: 0,
    waitingOn: 'ask',
    waitingUntil: '2026-10-09T13:00:00Z',
    runs: [{ nodeId: 'ask', status: 'waiting', startedAt: '2026-10-09T12:00:00Z' }],
  }

  const held: Schemas['ExecutionTask'] = {
    call: 'each',
    answerAs: 'fields',
    schema: {
      type: 'object',
      additionalProperties: false,
      properties: {
        watch: { type: 'boolean' },
        why: { anyOf: [{ type: 'string' }, { type: 'null' }] },
      },
      required: ['watch', 'why'],
    },
    items: [{ prompt: 'Look into Rain?', item: { question: 'Rain?' } }],
    until: '2026-10-09T13:00:00Z',
    heldBy: 'Claude Code',
    heldUntil: '2026-10-09T12:10:00Z',
  }

  it('says who has its task, hands it back, and answers it', async () => {
    let task = held
    let answered: unknown

    editing(
      workflow({
        nodes: [
          { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
          {
            id: 'ask',
            type: 'ai.localAgent',
            params: { prompt: 'Look into it' },
            position: { x: 300, y: 0 },
          },
        ],
        connections: [{ from: 'manual', fromPort: 'main', to: 'ask', toPort: 'main' }],
      }),
    )
    server.use(
      http.get('/api/v1/workflows/wf-1/executions', () =>
        HttpResponse.json({ executions: [onTask] }),
      ),
      http.get('/api/v1/workflows/wf-1/executions/ex-7', () => HttpResponse.json(onTask)),
      http.get('/api/v1/workflows/wf-1/executions/ex-7/task', () => HttpResponse.json(task)),
      http.post('/api/v1/workflows/wf-1/executions/ex-7/task/hand-back', () => {
        task = { ...held, heldBy: undefined, heldUntil: undefined }
        return new HttpResponse(null, { status: 204 })
      }),
      http.post('/api/v1/workflows/wf-1/executions/ex-7/task/answer', async ({ request }) => {
        answered = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'History' }))
    const list = within(await screen.findByRole('tabpanel', { name: 'Executions' }))
    await user.click((await list.findAllByRole('button'))[0])

    expect(await screen.findByText(/Claude Code has the task until/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Hand back' }))
    expect(
      await screen.findByText(/Waiting for one of your agents to take the task/),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Hand back' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Answer it' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('Look into Rain?')).toBeInTheDocument()
    await user.click(dialog.getByRole('checkbox', { name: 'watch' }))
    await user.type(dialog.getByLabelText('why (optional)'), 'Rain is likely')
    await user.click(dialog.getByRole('button', { name: 'Send answer' }))

    await waitFor(() =>
      expect(answered).toEqual({ answers: [{ watch: true, why: 'Rain is likely' }] }),
    )
    expect(await screen.findByText('Answered: the run carries on')).toBeInTheDocument()
  })
})

describe('workflow as code', () => {
  // What the server builds of the code: branching with tight's value changed and a node
  // after log. Code says nothing of positions.
  const built: Schemas['WorkflowDefinition'] = {
    name: 'Spread watch',
    nodes: [
      { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
      {
        id: 'tight',
        type: 'logic.if',
        params: { field: 'spread', value: '0.05' },
        position: { x: 0, y: 0 },
      },
      { id: 'log', type: 'output.log', position: { x: 0, y: 0 } },
      { id: 'more', type: 'output.log', params: { message: 'more' }, position: { x: 0, y: 0 } },
    ],
    connections: [
      ...branching().connections,
      { from: 'log', fromPort: 'main', to: 'more', toPort: 'main' },
    ],
  }

  // running answers the code the editor runs, and records it.
  function running(answer = () => HttpResponse.json({ workflow: built })) {
    const sources: string[] = []

    server.use(
      http.post('/api/v1/code/workflow', async ({ request }) => {
        sources.push(((await request.json()) as { source: string }).source)
        return answer()
      }),
    )

    return sources
  }

  const codeBox = () => screen.getByRole<HTMLTextAreaElement>('textbox', { name: 'Workflow code' })

  async function editCode(user: ReturnType<typeof renderApp>['user']) {
    await waitFor(() => canvasNode('tight'))
    await user.click(screen.getByRole('tab', { name: 'Code' }))
    await user.type(codeBox(), '{Enter}// more')
  }

  it('prints the workflow, and applies its code to the canvas, nodes keeping their place', async () => {
    const saves = editing(branching())
    const sources = running()
    const { user } = renderApp('/workflows/wf-1')

    await waitFor(() => canvasNode('tight'))
    await user.click(screen.getByRole('tab', { name: 'Code' }))

    expect(codeBox().value).toContain(
      'const tight = w.node("tight", "logic.if", { field: "spread", value: "0.02" })',
    )
    expect(codeBox().value).toContain('tight.port("true").to(log)')
    expect(screen.getByText('The canvas shows this code.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()

    await user.type(codeBox(), '{Enter}// more')
    expect(screen.getByText(/Not applied yet/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Apply' }))
    expect(await screen.findByText('The canvas shows this code.')).toBeInTheDocument()
    expect(sources).toHaveLength(1)
    expect(sources[0].endsWith('})\n\n// more')).toBe(true)
    expect(screen.getByText(/Unsaved changes/)).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: 'Canvas' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    await waitFor(() => canvasNode('more'))

    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Version 4')).toBeInTheDocument()
    expect(saves[0].nodes.map((n) => [n.id, n.position])).toEqual([
      ['manual', { x: 0, y: 0 }],
      ['tight', { x: 300, y: 0 }],
      ['log', { x: 600, y: 0 }],
      ['more', { x: 900, y: 0 }], // new: after the node feeding it
    ])
    expect(saves[0].nodes[1].params).toEqual({ field: 'spread', value: '0.05' })
  })

  it('asks before leaving the code unapplied: apply, discard or keep editing', async () => {
    editing(branching())
    const sources = running()
    const { user } = renderApp('/workflows/wf-1')

    await editCode(user)
    await user.click(screen.getByRole('tab', { name: 'Canvas' }))

    let dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Apply your code?')
    await user.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    expect(codeBox().value).toContain('// more')

    await user.click(screen.getByRole('tab', { name: 'Canvas' }))
    dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Discard' }))
    await waitFor(() => canvasNode('tight'))
    expect(() => canvasNode('more')).toThrow()
    expect(sources).toHaveLength(0)
    expect(screen.queryByText(/Unsaved changes/)).not.toBeInTheDocument()

    await editCode(user)
    await user.click(screen.getByRole('tab', { name: 'Canvas' }))
    dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Apply' }))
    await waitFor(() => canvasNode('more'))
    expect(sources).toHaveLength(1)
  })

  it('applies the code before saving it', async () => {
    const saves = editing(branching())
    const sources = running()
    const { user } = renderApp('/workflows/wf-1')

    await editCode(user)
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Version 4')).toBeInTheDocument()
    expect(sources).toHaveLength(1)
    expect(saves[0].nodes.map((n) => n.id)).toEqual(['manual', 'tight', 'log', 'more'])
    expect(screen.getByText('The canvas shows this code.')).toBeInTheDocument()
  })

  it('says why code was not applied, and where, and does not save it', async () => {
    const saves = editing(branching())
    running(() =>
      HttpResponse.json(
        {
          error: {
            code: 'invalid_code',
            message: 'line 4, column 9: Expected ")" but found "}"',
            problems: [{ line: 4, column: 9, message: 'Expected ")" but found "}"' }],
          },
        },
        { status: 400 },
      ),
    )
    const { user } = renderApp('/workflows/wf-1')

    await editCode(user)
    await user.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText(/^Not applied: line 4, column 9/)).toBeInTheDocument()
    expect(screen.getByText('4:9 Expected ")" but found "}"')).toBeInTheDocument()
    expect(screen.getByText(/Not applied yet/)).toBeInTheDocument()
    expect(saves).toHaveLength(0)

    // Editing the code clears what was wrong with it.
    await user.type(codeBox(), ' ')
    expect(screen.queryByText(/^Not applied: line 4/)).not.toBeInTheDocument()
  })

  it('shows the code to read when an admin turned code off, Apply off', async () => {
    editing(branching())
    server.use(http.get('/api/v1/me', () => HttpResponse.json({ ...alice, mayWriteCode: false })))
    const { user } = renderApp('/workflows/wf-1')

    await waitFor(() => canvasNode('tight'))
    await user.click(screen.getByRole('tab', { name: 'Code' }))

    expect(codeBox().value).toContain('w.trigger("manual", "trigger.manual")')
    expect(codeBox()).toHaveAttribute('readonly')
    expect(screen.getByRole('button', { name: 'Apply' })).toBeDisabled()
    expect(screen.getByText(/An admin has turned code off for you/)).toBeInTheDocument()
  })

  it('leaving the page with the code unapplied applies and saves it, if asked', async () => {
    const saves = editing(branching())
    const sources = running()
    const { router, user } = renderApp('/workflows/wf-1')

    await editCode(user)
    await user.click(within(screen.getByRole('main')).getByRole('link', { name: 'Workflows' }))

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Apply your code and save?')
    await user.click(within(dialog).getByRole('button', { name: 'Apply and save' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/workflows'))
    expect(sources).toHaveLength(1)
    expect(saves[0].nodes.map((n) => n.id)).toContain('more')
  })
})

// key presses a key on the page, as the canvas takes it: Ctrl, not on a Mac.
function key(k: string, code: string, mods: { ctrl?: boolean; shift?: boolean } = {}) {
  fireEvent.keyDown(document.body, { key: k, code, ctrlKey: mods.ctrl, shiftKey: mods.shift })
}

const onCanvas = (id: string) =>
  document.querySelector(`.react-flow__node[data-id="${id}"]`) !== null

// clipboard is the system's, as the browser's copy and paste events carry it.
function clipboard() {
  const data = new Map<string, string>()

  return {
    setData: (type: string, value: string) => void data.set(type, value),
    getData: (type: string) => data.get(type) ?? '',
  }
}

describe('workflow editor shortcuts (D420)', () => {
  it('undoes and redoes a deletion, a save clearing them', async () => {
    const saves = editing(branching())
    const { user } = renderApp('/workflows/wf-1')

    await selectNode('log')
    key('Delete', 'Delete')
    await waitFor(() => expect(onCanvas('log')).toBe(false))

    key('z', 'KeyZ', { ctrl: true })
    await waitFor(() => expect(onCanvas('log')).toBe(true))
    expect(screen.queryByText(/Unsaved changes/)).not.toBeInTheDocument()

    key('Z', 'KeyZ', { ctrl: true, shift: true })
    await waitFor(() => expect(onCanvas('log')).toBe(false))

    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(saves).toHaveLength(1))
    expect(saves[0].nodes.map((n) => n.id)).toEqual(['manual', 'tight'])
    expect(saves[0].connections, 'its connection went with it').toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Undo' })).toBeDisabled()
  })

  it('undoes typing in one field as one step, the field keeping its own keys', async () => {
    editing(branching())
    const { user } = renderApp('/workflows/wf-1')

    const name = await screen.findByRole('textbox', { name: 'Workflow name' })
    await user.type(name, ' v2')
    expect(name).toHaveValue(`${branching().name} v2`)

    await user.click(screen.getByRole('button', { name: 'Undo' }))
    expect(name).toHaveValue(branching().name)
    await user.click(screen.getByRole('button', { name: 'Redo' }))
    expect(name).toHaveValue(`${branching().name} v2`)
  })

  it('selects every node, and Escape clears them', async () => {
    editing(branching())
    renderApp('/workflows/wf-1')
    await waitFor(() => canvasNode('tight'))

    key('a', 'KeyA', { ctrl: true })
    await waitFor(() => expect(canvasNode('manual')).toHaveClass('selected'))
    expect(canvasNode('log')).toHaveClass('selected')

    key('Escape', 'Escape')
    await waitFor(() => expect(canvasNode('manual')).not.toHaveClass('selected'))
  })

  it('copies nodes to the clipboard and pastes them, with the connections between them', async () => {
    const saves = editing(branching())
    const { user } = renderApp('/workflows/wf-1')
    await waitFor(() => canvasNode('tight'))

    key('a', 'KeyA', { ctrl: true })
    await waitFor(() => expect(canvasNode('log')).toHaveClass('selected'))

    const data = clipboard()
    fireEvent.copy(document.body, { clipboardData: data })
    expect(JSON.parse(data.getData('text/plain'))).toMatchObject({ 'polythought/nodes': 1 })

    fireEvent.paste(document.body, { clipboardData: data })
    await waitFor(() => expect(onCanvas('tight2')).toBe(true))
    expect(canvasNode('tight2')).toHaveClass('selected')
    expect(canvasNode('tight')).not.toHaveClass('selected')

    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(saves).toHaveLength(1))
    expect(saves[0].nodes.map((n) => n.id)).toEqual([
      'manual',
      'tight',
      'log',
      'manual2',
      'tight2',
      'log2',
    ])
    expect(saves[0].nodes[4]).toMatchObject({
      params: { field: 'spread', value: '0.02' },
      position: { x: 340, y: 40 },
    })
    expect(saves[0].connections).toContainEqual({
      from: 'tight2',
      fromPort: 'true',
      to: 'log2',
      toPort: 'main',
    })
  })

  it('duplicates the selected nodes, and lists the shortcuts', async () => {
    editing(branching())
    renderApp('/workflows/wf-1')

    await selectNode('tight')
    key('d', 'KeyD', { ctrl: true })
    await waitFor(() => expect(onCanvas('tight2')).toBe(true))

    key('?', 'Slash', { shift: true })
    const dialog = await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })
    expect(within(dialog).getByText('Undo')).toBeInTheDocument()
    expect(within(dialog).getByText('Ctrl+Z')).toBeInTheDocument()
  })

  it('takes the keys the user set', async () => {
    editing(branching())
    server.use(
      http.get('/api/v1/me', () => HttpResponse.json({ ...alice, shortcuts: { undo: 'mod+u' } })),
    )
    renderApp('/workflows/wf-1')

    await selectNode('log')
    key('Delete', 'Delete')
    await waitFor(() => expect(onCanvas('log')).toBe(false))

    key('z', 'KeyZ', { ctrl: true })
    expect(onCanvas('log')).toBe(false)

    key('u', 'KeyU', { ctrl: true })
    await waitFor(() => expect(onCanvas('log')).toBe(true))
  })
})
