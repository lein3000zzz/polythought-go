import '@/test/browser'

import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
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
