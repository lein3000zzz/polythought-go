import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import type { Schemas } from './client'
import { keys } from './keys'

export type ExecutionEvent = Schemas['ExecutionEvent']

// How long to wait before opening a stream the server refused, for instance after the
// session ended or with too many tabs open. Dropped streams the browser retries itself.
const reopenAfter = 15_000

/**
 * Listens to the server's event stream while mounted and folds every finished execution
 * into the cache, so open execution lists update as runs happen.
 */
export function useLiveEvents() {
  const queryClient = useQueryClient()

  useEffect(() => {
    let source: EventSource | undefined
    let reopen: ReturnType<typeof setTimeout> | undefined
    let stopped = false

    const open = () => {
      source = new EventSource('/api/v1/events')

      source.addEventListener('execution', (e) => {
        applyExecution(queryClient, JSON.parse((e as MessageEvent<string>).data) as ExecutionEvent)
      })

      source.addEventListener('error', () => {
        if (source?.readyState !== EventSource.CLOSED || stopped) return

        // A refusal may mean the session ended: asking who is signed in finds out.
        void queryClient.invalidateQueries({ queryKey: keys.me })
        reopen = setTimeout(open, reopenAfter)
      })
    }

    open()

    return () => {
      stopped = true
      clearTimeout(reopen)
      source?.close()
    }
  }, [queryClient])
}

/** The number of executions an execution list keeps, matching what it fetches. */
export const executionListSize = 20

/**
 * Puts a finished execution where the cache would show it: at the top of its
 * workflow's list, if that list is loaded, and as the execution itself unless a fuller
 * copy, with the item samples a manual run keeps, is cached already.
 */
export function applyExecution(
  queryClient: QueryClient,
  { workflowId, execution }: ExecutionEvent,
) {
  queryClient.setQueryData<Schemas['Execution'][]>(keys.executions(workflowId), (list) =>
    list && !list.some((e) => e.id === execution.id)
      ? [execution, ...list].slice(0, executionListSize)
      : list,
  )

  const detail = keys.execution(workflowId, execution.id)
  if (queryClient.getQueryData(detail) === undefined) {
    queryClient.setQueryData(detail, execution)
  }
}
