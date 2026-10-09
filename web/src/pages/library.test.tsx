import '@/test/browser'

import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { apiError, branching, nodeTypes, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

type Template = Schemas['Template']

const webhookType: Schemas['NodeType'] = {
  name: 'output.webhook',
  label: 'Webhook',
  trigger: false,
  inputs: ['main'],
  outputs: ['main'],
  params: [{ name: 'url', label: 'Webhook URL', kind: 'string', required: true, sensitive: true }],
}

const alerts: Template = {
  id: 'chat-alerts',
  name: 'Alerts in Discord or Slack',
  summary: 'Get a message when odds cross a line.',
  description:
    'Workflows can tell you things.\n\nTo get a webhook URL:\n- Discord: copy it\n- Slack: add the app',
  builtIn: true,
  mine: false,
  nodeTypes: ['trigger.manual', 'output.webhook'],
  inputs: [{ nodeId: 'chat', param: webhookType.params[0] }],
  nodes: [
    { id: 'manual', type: 'trigger.manual', notes: 'Starts it', position: { x: 0, y: 0 } },
    {
      id: 'chat',
      type: 'output.webhook',
      name: 'Post to the channel',
      notes: 'Sends one message',
      position: { x: 300, y: 0 },
    },
  ],
  connections: [{ from: 'manual', fromPort: 'main', to: 'chat', toPort: 'main' }],
}

const mineShared: Template = {
  id: 'tpl-1',
  name: 'My pings',
  summary: '',
  description: '',
  builtIn: false,
  mine: true,
  nodeTypes: ['trigger.schedule'],
  inputs: [],
  nodes: [{ id: 'every', type: 'trigger.manual' }],
  connections: [],
}

function library(templates: Template[] = [alerts, mineShared]) {
  server.use(
    signedIn,
    http.get('/api/v1/node-types', () =>
      HttpResponse.json({ nodeTypes: [...nodeTypes, webhookType] }),
    ),
    http.get('/api/v1/library', () =>
      HttpResponse.json({ templates: templates.map(({ nodes: _n, connections: _c, ...t }) => t) }),
    ),
    http.get('/api/v1/library/:id', ({ params }) => {
      const t = templates.find((x) => x.id === params.id)
      return t ? HttpResponse.json(t) : apiError(404, 'not_found', 'not found')
    }),
  )
}

describe('library', () => {
  it('lists scenarios apart from what members shared', async () => {
    library()
    renderApp('/library')

    const scenarios = (await screen.findByRole('heading', { name: 'Scenarios' })).closest(
      'section',
    )!
    expect(within(scenarios).getByText('Alerts in Discord or Slack')).toBeInTheDocument()
    expect(within(scenarios).getByText('Asks for a webhook URL')).toBeInTheDocument()

    const shared = screen.getByRole('heading', { name: 'Shared by members' }).closest('section')!
    expect(within(shared).getByText('My pings')).toBeInTheDocument()
    expect(within(shared).getByText('Yours')).toBeInTheDocument()
    expect(within(shared).getByText('Runs on its own')).toBeInTheDocument()
  })

  it('makes a workflow from a scenario with the values it asks for', async () => {
    const sent: unknown[] = []
    library()
    server.use(
      http.post('/api/v1/library/chat-alerts/use', async ({ request }) => {
        const body = await request.json()
        sent.push(body)

        return sent.length === 1
          ? HttpResponse.json(
              {
                error: {
                  code: 'invalid_workflow',
                  message: 'invalid workflow',
                  problems: [
                    {
                      nodeId: 'chat',
                      param: 'url',
                      message: 'node "chat": missing required param "url"',
                    },
                  ],
                },
              },
              { status: 400 },
            )
          : HttpResponse.json({ ...branching(), id: 'wf-7' }, { status: 201 })
      }),
      http.get('/api/v1/workflows/wf-7', () => HttpResponse.json({ ...branching(), id: 'wf-7' })),
      http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [] })),
    )
    const { router, user } = renderApp('/library/chat-alerts')

    expect(await screen.findByText('Workflows can tell you things.')).toBeInTheDocument()
    expect(screen.getByText('Slack: add the app').tagName).toBe('LI')

    await user.click(screen.getByRole('button', { name: 'Use this' }))
    const dialog = within(await screen.findByRole('dialog'))
    expect(dialog.getByText('For Post to the channel')).toBeInTheDocument()

    await user.click(dialog.getByRole('button', { name: 'Create workflow' }))
    expect(await dialog.findByText('Missing required param "url"')).toBeInTheDocument()

    await user.clear(dialog.getByLabelText('Name'))
    await user.type(dialog.getByLabelText('Name'), 'My alerts')
    await user.type(dialog.getByLabelText(/Webhook URL/), 'https://discord.com/api/webhooks/1/x')
    await user.click(dialog.getByRole('button', { name: 'Create workflow' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/workflows/wf-7'))
    expect(sent[1]).toEqual({
      name: 'My alerts',
      values: { chat: { url: 'https://discord.com/api/webhooks/1/x' } },
    })
  })

  it('shows what each node does', async () => {
    library()
    renderApp('/library/chat-alerts')

    // A plain click: user-event's mousedown lacks what React Flow's drag handling reads.
    const node = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('.react-flow__node[data-id="chat"]')
      if (!el) throw new Error('not drawn yet')
      return el
    })
    fireEvent.click(node)

    const about = within(screen.getByRole('complementary', { name: 'About' }))
    expect(about.getByText('Sends one message')).toBeInTheDocument()
    expect(about.getByText('You fill this in')).toBeInTheDocument()
  })

  it('lets you remove what you shared', async () => {
    let removed = false
    library()
    server.use(
      http.delete('/api/v1/library/tpl-1', () => {
        removed = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { router, user } = renderApp('/library/tpl-1')

    await user.click(await screen.findByRole('button', { name: 'Remove from library' }))
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Remove' }),
    )

    await waitFor(() => expect(router.state.location.pathname).toBe('/library'))
    expect(removed).toBe(true)
  })

  it('built-in scenarios cannot be removed', async () => {
    library()
    renderApp('/library/chat-alerts')

    await screen.findByRole('button', { name: 'Use this' })
    expect(screen.queryByRole('button', { name: 'Remove from library' })).not.toBeInTheDocument()
  })
})
