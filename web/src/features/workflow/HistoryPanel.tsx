import { cn } from 'cn'
import { X } from 'lucide-react'
import { useId, useState } from 'react'

import { useExecutions } from '@/api/executions'
import { type SwitchKind, switchKinds } from '@/api/history'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { actorSays } from '@/lib/actors'
import { formatDuration, formatTime } from '@/lib/format'
import { useStoredList } from '@/lib/storage'

import { ExecutionStatus } from './ExecutionStatus'
import { SwitchList } from './SwitchList'

type Page = 'executions' | 'history'

const pageNames: Record<Page, string> = { executions: 'Executions', history: 'History' }

const kindLabels: Record<SwitchKind, string> = {
  on_off: 'On and off',
  trading: 'Paper and live',
  reset: 'Paper resets',
  kill_switch: 'Kill switch',
}

type Props = {
  workflowId: string
  selected?: string
  onSelect: (id: string) => void
  follow: boolean // show each new execution as it finishes
  onFollow: (follow: boolean) => void
  onClose: () => void
}

/**
 * The workflow's history (D433), on two pages: its latest executions first, kept current by
 * the live event stream, picking one showing it on the canvas; and who switched it and
 * why, narrowed to the kinds ticked, kept for the next visit.
 */
export function HistoryPanel({ workflowId, selected, onSelect, follow, onFollow, onClose }: Props) {
  const [page, setPage] = useState<Page>('executions')
  const [kinds, setKinds] = useStoredList('polythought.historyKinds', [...switchKinds])
  const id = useId()

  const shown = switchKinds.filter((k) => kinds.includes(k))
  const tick = (k: SwitchKind) =>
    setKinds(shown.includes(k) ? shown.filter((x) => x !== k) : [...shown, k])

  return (
    <section aria-label="History" className="flex h-full flex-col border-t bg-background">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b px-4 py-1.5">
        <div role="tablist" aria-label="History pages" className="flex gap-1">
          {(Object.keys(pageNames) as Page[]).map((p) => (
            <Button
              key={p}
              id={`${id}-${p}`}
              role="tab"
              aria-selected={page === p}
              aria-controls={`${id}-panel`}
              variant={page === p ? 'secondary' : 'ghost'}
              size="sm"
              className="h-7"
              onClick={() => setPage(p)}
            >
              {pageNames[p]}
            </Button>
          ))}
        </div>

        <div className="flex items-center gap-3">
          {page === 'executions' ? (
            <div className="flex items-center gap-2">
              <Switch id="follow-runs" size="sm" checked={follow} onCheckedChange={onFollow} />
              <Label htmlFor="follow-runs" className="text-xs font-normal">
                Show new runs as they finish
              </Label>
            </div>
          ) : (
            <div role="group" aria-label="Show" className="flex flex-wrap gap-1">
              {switchKinds.map((k) => (
                <Button
                  key={k}
                  variant={shown.includes(k) ? 'secondary' : 'ghost'}
                  size="sm"
                  className="h-7 text-xs"
                  aria-pressed={shown.includes(k)}
                  onClick={() => tick(k)}
                >
                  {kindLabels[k]}
                </Button>
              ))}
            </div>
          )}
          <Button variant="ghost" size="icon-sm" aria-label="Close history" onClick={onClose}>
            <X />
          </Button>
        </div>
      </div>

      <div
        id={`${id}-panel`}
        role="tabpanel"
        aria-labelledby={`${id}-${page}`}
        className="min-h-0 flex-1 overflow-y-auto"
      >
        {page === 'executions' ? (
          <ExecutionList workflowId={workflowId} selected={selected} onSelect={onSelect} />
        ) : (
          <SwitchList workflowId={workflowId} kinds={shown} />
        )}
      </div>
    </section>
  )
}

function ExecutionList({
  workflowId,
  selected,
  onSelect,
}: Pick<Props, 'workflowId' | 'selected' | 'onSelect'>) {
  const executions = useExecutions(workflowId)

  return (
    <>
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
              {e.by && (
                <span className="max-w-64 truncate text-muted-foreground">{actorSays(e.by)}</span>
              )}
              {e.error && (
                <span className="min-w-0 flex-1 truncate text-destructive">{e.error}</span>
              )}
            </button>
          </li>
        ))}
      </ul>
    </>
  )
}
