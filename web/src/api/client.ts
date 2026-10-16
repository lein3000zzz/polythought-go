import createClient from 'openapi-fetch'

import type { components, paths } from './schema'

export type Schemas = components['schemas']

export type User = Schemas['User']
export type Workflow = Schemas['Workflow']
export type WorkflowNode = Schemas['Node']
export type Connection = Schemas['Connection']
export type NodeType = Schemas['NodeType']
export type ParamSpec = Schemas['ParamSpec']
export type Problem = Schemas['Problem']

// The API is on the same origin: Vite proxies it in development, Caddy in production. The
// URL is absolute so requests also work outside a browser, in tests.
export const api = createClient<paths>({
  baseUrl: new URL('/api/v1', window.location.origin).href,
})

/**
 * A failed API call. `code` is the server's stable error code, or `http_<status>` when the
 * response was not one of its JSON errors (a proxy's error page, say).
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly problems: Problem[]
  /** The workflows a change to a custom node would break (code breaks_workflows). */
  readonly workflows: Schemas['BrokenWorkflow'][]

  constructor(status: number, body: unknown) {
    const detail = errorDetail(body)

    super(detail?.message ?? `The server answered ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.code = detail?.code ?? `http_${status}`
    this.problems = detail?.problems ?? []
    this.workflows = detail?.workflows ?? []
  }
}

function errorDetail(body: unknown): Schemas['ErrorDetail'] | undefined {
  if (typeof body !== 'object' || body === null || !('error' in body)) return undefined

  const detail = body.error
  if (typeof detail !== 'object' || detail === null) return undefined
  if (!('code' in detail) || typeof detail.code !== 'string') return undefined
  if (!('message' in detail) || typeof detail.message !== 'string') return undefined

  return detail as Schemas['ErrorDetail']
}

type Result<T> = { data?: T; error?: unknown; response: Response }

/** Awaits an openapi-fetch request and returns its data, throwing ApiError on an error status. */
export async function call<T>(request: Promise<Result<T>>): Promise<T> {
  const { data, error, response } = await request
  if (!response.ok) throw new ApiError(response.status, error)

  return data as T
}

/** A message for people: the server's own, or a hint when the server was not reached. */
export function errorMessage(error: unknown): string {
  if (!(error instanceof ApiError)) {
    return 'Could not reach the server. Check your connection and try again.'
  }

  const message = error.message
  return message.charAt(0).toUpperCase() + message.slice(1)
}
