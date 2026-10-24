import { http, HttpResponse } from 'msw'

import type { Schemas } from '@/api/client'

export const alice: Schemas['User'] = {
  id: 'u-1',
  email: 'alice@example.com',
  role: 'user',
  emailVerified: true,
  disabled: false,
  codeChosen: false,
  mayWriteCode: true,
  liveStopped: false,
  liveCaps: {},
  agentCaps: {},
  killStopsAgents: false,
  agentsSeePersonal: false,
  agentsMayDelete: false,
  agentsMayShare: false,
  agentsMayUseCredentials: false,
  agentsMayAnswer: false,
  liveConfirm: {
    goLive: true,
    run: true,
    change: true,
    killOff: true,
    inAgent: false,
    role: true,
    disable: true,
    caps: true,
    code: true,
  },
  createdAt: '2026-10-01T10:00:00Z',
}

export function workflow(overrides: Partial<Schemas['Workflow']> = {}): Schemas['Workflow'] {
  return {
    id: 'wf-1',
    name: 'Spread watch',
    active: false,
    version: 1,
    nodes: [
      { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
      { id: 'log', type: 'output.log', position: { x: 300, y: 0 } },
    ],
    connections: [{ from: 'manual', fromPort: 'main', to: 'log', toPort: 'main' }],
    createdAt: '2026-10-01T10:00:00Z',
    updatedAt: '2026-10-02T10:00:00Z',
    ...overrides,
  }
}

export function apiError(status: number, code: string, message: string) {
  return HttpResponse.json({ error: { code, message } }, { status })
}

export const signedIn = http.get('/api/v1/me', () => HttpResponse.json(alice))

export const signedOut = http.get('/api/v1/me', () =>
  apiError(401, 'unauthenticated', 'not authenticated'),
)

export function registration(
  mode: Schemas['AuthConfig']['registration'],
  minPasswordLength = 12,
  requests = false,
) {
  return http.get('/api/v1/auth/config', () =>
    HttpResponse.json({ registration: mode, minPasswordLength, passwordReset: false, requests }),
  )
}

/** Node types as the server describes them, a representative few. */
export const nodeTypes: Schemas['NodeType'][] = [
  {
    name: 'trigger.manual',
    label: 'Manual',
    trigger: true,
    code: false,
    inputs: [],
    outputs: ['main'],
    params: [],
  },
  {
    name: 'logic.if',
    label: 'If',
    description: 'Compares a numeric field of each item with a value.',
    trigger: false,
    code: false,
    inputs: ['main'],
    outputs: ['true', 'false'],
    params: [
      { name: 'field', label: 'Field', kind: 'string', required: true },
      {
        name: 'operator',
        label: 'Operator',
        kind: 'string',
        required: false,
        default: 'gt',
        options: ['gt', 'lt'],
      },
      { name: 'value', label: 'Value', kind: 'decimal', required: true },
    ],
  },
  {
    name: 'output.log',
    label: 'Log',
    trigger: false,
    code: false,
    inputs: ['main'],
    outputs: ['main'],
    params: [{ name: 'message', label: 'Message', kind: 'string', required: false }],
  },
  {
    name: 'polymarket.balance',
    label: 'Balance',
    trigger: false,
    code: false,
    inputs: ['main'],
    outputs: ['main'],
    params: [{ name: 'wallet', label: 'Wallet', kind: 'wallet', required: true }],
  },
]

/** manual -> tight (an If node) -> log, saved as version 3. */
export function branching(overrides: Partial<Schemas['Workflow']> = {}): Schemas['Workflow'] {
  return workflow({
    version: 3,
    nodes: [
      { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
      {
        id: 'tight',
        type: 'logic.if',
        params: { field: 'spread', value: '0.02' },
        position: { x: 300, y: 0 },
      },
      { id: 'log', type: 'output.log', position: { x: 600, y: 0 } },
    ],
    connections: [
      { from: 'manual', fromPort: 'main', to: 'tight', toPort: 'main' },
      { from: 'tight', fromPort: 'true', to: 'log', toPort: 'main' },
    ],
    ...overrides,
  })
}

/** A wallet's trading approvals, all granted: for tests about something else. */
export const approvalsGranted = http.get('/api/v1/wallets/:id/approvals', () =>
  HttpResponse.json({ ready: true, missing: [], checkedAt: '2026-10-19T12:00:00Z', way: 'none' }),
)
