import '@/test/browser'

import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { FakeEventSource } from '@/test/eventSource'
import { apiError, branching, nodeTypes, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

type Saved = Schemas['UpdateWorkflowRequest']

// editing serves one workflow and records what the editor saves.
function editing(workflow: Schemas['Workflow']) {
  const saves: Saved[] = []

  server.use(
    signedIn,
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
