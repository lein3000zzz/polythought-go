import { TriangleAlert } from 'lucide-react'

import { useCodeRunners } from '@/api/settings'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Skeleton } from '@/components/ui/skeleton'

/**
 * Where user code runs, and how its runner processes are, as the server started (D357):
 * amber while they are not sandboxed, red while they would not start, failing every code
 * run (D358).
 */
export function CodeRunners() {
  const runners = useCodeRunners()

  if (runners.isPending) return <Skeleton className="h-5 w-72" />
  if (runners.isError) return <ErrorAlert error={runners.error} />

  const r = runners.data
  const said = (text: string) => text.charAt(0).toUpperCase() + text.slice(1)

  if (!r.inRunners) {
    return (
      <p className="border-t pt-4 text-sm text-muted-foreground">
        Code runs in the server&apos;s process.
      </p>
    )
  }

  if (r.failed) {
    return (
      <p className="flex gap-2 border-t pt-4 text-sm text-destructive">
        <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden />
        <span>The runner processes would not start, so code does not run: {r.failed}</span>
      </p>
    )
  }

  return (
    <div className="flex flex-col gap-1 border-t pt-4 text-sm">
      {r.sandboxed ? (
        <p>Code runs in {r.size} runner processes, sandboxed.</p>
      ) : (
        <p className="flex gap-2">
          <TriangleAlert
            className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-500"
            aria-hidden
          />
          <span>
            Code runs in {r.size} runner processes, not sandboxed. {said(r.why)}.
          </span>
        </p>
      )}
      {r.running < r.size && (
        <p className="text-muted-foreground">
          {r.running} of them running now: the others start again with their next run.
        </p>
      )}
      {r.restarts > 0 && (
        <p className="text-muted-foreground">
          {r.restarts} restarted since the server started
          {r.lastError ? `: ${r.lastError}` : ''}.
        </p>
      )}
    </div>
  )
}
