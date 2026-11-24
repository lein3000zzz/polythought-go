import '@/test/browser'

import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, nodeTypes } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const root: Schemas['User'] = { ...alice, id: 'u-0', email: 'root@example.com', role: 'admin' }
const counts = (runs = 0, failed = 0) => ({ runs, failed })

const webhookType: Schemas['NodeType'] = {
  name: 'output.webhook',
  label: 'Webhook',
  trigger: false,
  code: false,
  inputs: ['main'],
  outputs: ['main'],
  params: [{ name: 'url', label: 'Webhook URL', kind: 'string', required: true, sensitive: true }],
}

const busy: Schemas['AdminWorkflow'] = {
  id: 'wf-1',
  name: 'Busy one',
  ownerId: 'u-1',
  ownerEmail: 'alice@example.com',
  active: true,
  triggers: ['trigger.schedule'],
  hour: counts(3000, 4),
  day: counts(9000),
  week: counts(9000),
  last: { id: 'ex-9', status: 'succeeded', startedAt: '2026-11-24T11:59:00Z' },
}

const quiet: Schemas['AdminWorkflow'] = {
  ...busy,
  id: 'wf-2',
  name: 'Quiet one',
  active: false,
  hold: { reason: 'it looped', at: '2026-11-24T10:00:00Z' },
  hour: counts(),
  day: counts(2),
  week: counts(30),
  last: undefined,
}

const workflow = (more: Partial<Schemas['Workflow']> = {}): Schemas['Workflow'] => ({
  id: 'wf-1',
  name: 'Busy one',
  description: '',
  active: true,
  version: 3,
  nodes: [
    { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
    {
      id: 'hook',
      type: 'output.webhook',
      params: { url: { $personal: 'hidden' } },
      position: { x: 300, y: 0 },
    },
  ],
  connections: [{ from: 'manual', fromPort: 'main', to: 'hook', toPort: 'main' }],
  createdAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-11-01T00:00:00Z',
  ...more,
})

// oversee serves an admin every workflow and wf-1 itself, keeping what is switched.
function oversee() {
  const sent: { path: string; body?: unknown }[] = []
  let held: Schemas['WorkflowHold'] | undefined

  server.use(
    http.get('/api/v1/me', () => HttpResponse.json(root)),
    http.get('/api/v1/node-types', () =>
      HttpResponse.json({ nodeTypes: [...nodeTypes, webhookType] }),
    ),
    http.get('/api/v1/admin/workflows', () =>
      HttpResponse.json({ workflows: [busy, quiet], at: '2026-11-24T12:00:00Z' }),
    ),
    http.get('/api/v1/admin/users/u-1/workflows/wf-1', () =>
      HttpResponse.json(workflow({ hold: held, active: !held })),
    ),
    http.get('/api/v1/admin/users/u-1/workflows/wf-1/executions', () =>
      HttpResponse.json({
        executions: [
          {
            id: 'ex-9',
            workflowId: 'wf-1',
            workflowVersion: 3,
            status: 'failed',
            triggeredBy: 'every',
            startedAt: '2026-11-24T11:59:00Z',
            finishedAt: '2026-11-24T11:59:01Z',
            durationMs: 1000,
            error: 'node "hook": 500',
            runs: [],
          },
        ],
      }),
    ),
    http.get('/api/v1/admin/users/u-1/workflows/wf-1/history', () =>
      HttpResponse.json({
        switches: [
          {
            id: 'sw-1',
            kind: 'hold',
            admin: true,
            polythought: false,
            reason: 'it looped',
            at: '2026-11-24T10:00:00Z',
          },
        ],
      }),
    ),
    http.post('/api/v1/admin/users/:uid/workflows/:id/switch-off', async ({ request, params }) => {
      const body = (await request.json()) as { reason: string; hold: boolean }
      sent.push({ path: `switch-off ${params.uid as string}/${params.id as string}`, body })
      held = body.hold ? { reason: body.reason, at: '2026-11-24T12:01:00Z' } : undefined

      return HttpResponse.json(workflow({ active: false, hold: held }))
    }),
    http.post('/api/v1/admin/users/:uid/workflows/:id/release', ({ params }) => {
      sent.push({ path: `release ${params.uid as string}/${params.id as string}` })
      held = undefined

      return HttpResponse.json(workflow({ active: false }))
    }),
  )

  return sent
}

const rowNames = (table: HTMLElement) =>
  within(table)
    .getAllByRole('row')
    .slice(1)
    .map((r) => within(r).getAllByRole('cell')[0].textContent)

describe('admin oversight (D435)', () => {
  it('lists every workflow, the busiest first, held ones said so', async () => {
    oversee()
    renderApp('/admin/stats?tab=workflows')

    const table = await screen.findByRole('table', { name: "Every user's workflows" })
    expect(screen.getByRole('tab', { name: 'Workflows' })).toHaveAttribute('aria-selected', 'true')
    expect(rowNames(table)).toEqual(['Busy oneActive', 'Quiet oneHeld'])

    const busyRow = within(table).getByRole('row', { name: /Busy one/ })
    expect(within(busyRow).getAllByRole('cell')[2]).toHaveTextContent('3000')
    expect(within(busyRow).getByRole('link', { name: 'Busy one' })).toHaveAttribute(
      'href',
      '/admin/users/u-1/workflows/wf-1',
    )
    expect(within(table).getByRole('row', { name: /Quiet one/ })).toHaveTextContent('Release')
  })

  it('switches one off with a reason, held, and releases it', async () => {
    const sent = oversee()
    const { user } = renderApp('/admin/stats?tab=workflows')

    const table = await screen.findByRole('table', { name: "Every user's workflows" })
    await user.click(
      within(within(table).getByRole('row', { name: /Busy one/ })).getByRole('button', {
        name: 'Switch off',
      }),
    )

    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent('alice@example.com is told why')
    expect(within(dialog).getByRole('button', { name: 'Switch off' })).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Why'), 'It ran 3,000 times in an hour.')
    expect(within(dialog).getByRole('checkbox')).toBeChecked()
    await user.click(within(dialog).getByRole('button', { name: 'Switch off' }))

    await waitFor(() =>
      expect(sent).toEqual([
        {
          path: 'switch-off u-1/wf-1',
          body: { reason: 'It ran 3,000 times in an hour.', hold: true },
        },
      ]),
    )

    await user.click(
      within(within(table).getByRole('row', { name: /Quiet one/ })).getByRole('button', {
        name: 'Release',
      }),
    )
    await waitFor(() => expect(sent.at(-1)).toEqual({ path: 'release u-1/wf-2' }))
  })

  it("opens a user's workflow read-only: settings hidden, runs and history", async () => {
    oversee()
    const { user } = renderApp('/admin/users/u-1/workflows/wf-1')

    expect(await screen.findByRole('heading', { name: 'Busy one' })).toBeInTheDocument()
    expect(screen.getByText('alice@example.com')).toBeInTheDocument()

    const node = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('.react-flow__node[data-id="hook"]')
      if (!el) throw new Error('not drawn yet')
      return el
    })
    fireEvent.click(node)
    expect(
      within(screen.getByRole('complementary', { name: 'Node' })).getByText(
        'Hidden: a personal setting',
      ),
    ).toBeInTheDocument()

    const runs = screen.getByRole('tabpanel', { name: 'Runs' })
    expect(within(runs).getByText('node "hook": 500')).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: 'History' }))
    expect(
      await within(screen.getByRole('tabpanel', { name: 'History' })).findByText('Held off'),
    ).toBeInTheDocument()
    expect(screen.getByText(/by an admin: it looped/)).toBeInTheDocument()
  })

  it('holds it off from its page, which then says so and offers release', async () => {
    oversee()
    const { user } = renderApp('/admin/users/u-1/workflows/wf-1')

    await user.click(await screen.findByRole('button', { name: 'Switch off' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('Why'), 'It looped.')
    await user.click(within(dialog).getByRole('button', { name: 'Switch off' }))

    expect(
      await screen.findByText(/Held off by an admin since .*: It looped\./),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Release' })).toBeInTheDocument()
  })
})
