import { describe, expect, it } from 'vitest'

import { actorName, actorSays } from './actors'

describe('who acted for you (D407)', () => {
  it('names an agent by its name, a token by its', () => {
    expect(actorName({ kind: 'agent', name: 'Claude Code', how: 'run' })).toBe('Claude Code')
    expect(actorName({ kind: 'token', name: 'deploy', how: 'run' })).toBe('the API token “deploy”')
  })

  it('says how, and that you confirmed it', () => {
    expect(actorSays({ kind: 'agent', name: 'Claude Code', how: 'switched_on' })).toBe(
      'switched on by Claude Code',
    )
    expect(actorSays({ kind: 'token', name: 'deploy', how: 'run', confirmed: true })).toBe(
      'run by the API token “deploy”, confirmed by you',
    )
    expect(actorSays({ kind: 'agent', name: 'Codex', how: 'changed' })).toBe('changed by Codex')
  })
})
