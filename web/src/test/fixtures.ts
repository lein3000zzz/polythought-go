import { http, HttpResponse } from 'msw'

import type { Schemas } from '@/api/client'

export const alice: Schemas['User'] = {
  id: 'u-1',
  email: 'alice@example.com',
  role: 'user',
  emailVerified: false,
  disabled: false,
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

export function registration(mode: Schemas['AuthConfig']['registration']) {
  return http.get('/api/v1/auth/config', () => HttpResponse.json({ registration: mode }))
}
