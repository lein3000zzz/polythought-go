import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import type { Schemas } from './client'
import { keys } from './keys'

export type ExecutionEvent = Schemas['ExecutionEvent']

// How long to wait before opening a stream the server refused, for instance after the
// session ended or with too many tabs open. Dropped streams the browser retries itself.
const reopenAfter = 15_000

/**
 * Listens to the server's event stream while mounted and folds every execution that
 * finished or waits into the cache, so open execution lists update as runs happen.
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
 * Puts an execution that finished, or started or stopped waiting, where the cache would
 * show it: in its workflow's list, if that list is loaded, at the top when new; and as the
 * execution itself. A cached copy that changed is fetched again rather than replaced, as
 * the server's has the item samples a manual run keeps.
 */
export function applyExecution(
  queryClient: QueryClient,
  { workflowId, execution }: ExecutionEvent,
) {
  queryClient.setQueryData<Schemas['Execution'][]>(keys.executions(workflowId), (list) => {
    if (!list) return list
    if (list.some((e) => e.id === execution.id)) {
      return list.map((e) => (e.id === execution.id ? execution : e))
    }

    return [execution, ...list].slice(0, executionListSize)
  })

  const detail = keys.execution(workflowId, execution.id)
  const cached = queryClient.getQueryData<Schemas['Execution']>(detail)

  if (cached === undefined) {
    queryClient.setQueryData(detail, execution)
  } else if (cached.status !== execution.status) {
    void queryClient.invalidateQueries({ queryKey: detail })
  }
}
