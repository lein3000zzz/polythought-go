import { cn } from 'cn'
import { X } from 'lucide-react'

import { useExecutions } from '@/api/executions'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { formatDuration, formatTime } from '@/lib/format'

import { ExecutionStatus } from './ExecutionStatus'

type Props = {
  workflowId: string
  selected?: string
  onSelect: (id: string) => void
  follow: boolean // show each new execution as it finishes
  onFollow: (follow: boolean) => void
  onClose: () => void
}

/**
 * The workflow's latest executions, kept current by the live event stream; picking one
 * shows it on the canvas.
 */
export function ExecutionsPanel({
  workflowId,
  selected,
  onSelect,
  follow,
  onFollow,
  onClose,
}: Props) {
  const executions = useExecutions(workflowId)

  return (
    <section aria-label="Executions" className="flex h-full flex-col border-t bg-background">
      <div className="flex items-center justify-between border-b px-4 py-1.5">
        <h2 className="text-sm font-medium">Executions</h2>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <Switch id="follow-runs" size="sm" checked={follow} onCheckedChange={onFollow} />
            <Label htmlFor="follow-runs" className="text-xs font-normal">
              Show new runs as they finish
            </Label>
          </div>
          <Button variant="ghost" size="icon-sm" aria-label="Close executions" onClick={onClose}>
            <X />
          </Button>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {executions.isError && (
          <div className="p-3">
            <ErrorAlert error={executions.error} onRetry={() => void executions.refetch()} />
          </div>
        )}

        {executions.data?.length === 0 && (
          <p className="p-4 text-sm text-muted-foreground">
            No executions yet. Run the workflow, or activate it to let its triggers start it.
          </p>
        )}

        <ul>
          {executions.data?.map((e) => (
            <li key={e.id}>
              <button
                type="button"
                onClick={() => onSelect(e.id)}
                aria-current={e.id === selected || undefined}
                className={cn(
                  'flex w-full items-center gap-4 px-4 py-1.5 text-left text-sm hover:bg-muted',
                  e.id === selected && 'bg-muted',
                )}
              >
                <ExecutionStatus status={e.status} />
                <span className="w-24 tabular-nums">{formatTime(e.startedAt)}</span>
                <span className="w-20 text-muted-foreground tabular-nums">
                  {e.finishedAt
                    ? formatDuration(e.durationMs)
                    : e.waitingUntil
                      ? `till ${formatTime(e.waitingUntil)}`
                      : '…'}
                </span>
                <span className="w-28 truncate text-muted-foreground">{e.triggeredBy}</span>
                <span className="w-20 text-muted-foreground">v{e.workflowVersion}</span>
                {e.error && (
                  <span className="min-w-0 flex-1 truncate text-destructive">{e.error}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      </div>
    </section>
  )
}
