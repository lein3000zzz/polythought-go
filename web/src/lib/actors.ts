import type { Schemas } from '@/api/client'

export type Actor = Schemas['Actor']

/** An agent by its name, a token by its (D407). */
export function actorName(a: Actor): string {
  return a.kind === 'agent' ? a.name : `the API token “${a.name}”`
}

/** What the agent or token did, said for you: "switched on by Claude Code, confirmed by you". */
export function actorSays(a: Actor): string {
  const how = { run: 'run by', switched_on: 'switched on by', changed: 'changed by' }[a.how]
  return `${how} ${actorName(a)}${a.confirmed ? ', confirmed by you' : ''}`
}
