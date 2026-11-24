import { ArrowLeft } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { toast } from 'sonner'

import {
  useAdminExecutions,
  useAdminRelease,
  useAdminWorkflow,
  useAdminWorkflows,
} from '@/api/admin'
import { ApiError, errorMessage } from '@/api/client'
import { switchKinds } from '@/api/history'
import { useNodeTypes } from '@/api/workflows'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { SwitchOffDialog } from '@/features/admin/SwitchOffDialog'
import { ExecutionStatus } from '@/features/workflow/ExecutionStatus'
import { NodeSettings } from '@/features/workflow/NodeSettings'
import { SwitchList } from '@/features/workflow/SwitchList'
import { TemplateCanvas } from '@/features/workflow/TemplateCanvas'
import { formatDateTime, formatDuration } from '@/lib/format'

type Tab = 'runs' | 'history'

/**
 * One of a user's workflows, for an admin (D435): read-only, its personal settings hidden,
 * its runs listed without the items they carried, and who switched it; switched off with
 * a reason, held off, or released.
 */
export function AdminWorkflowPage() {
  const { uid = '', id = '' } = useParams()
  const workflow = useAdminWorkflow(uid, id)
  const nodeTypes = useNodeTypes()
  const listed = useAdminWorkflows().data?.workflows.find((w) => w.id === id)
  const release = useAdminRelease()
  const [selected, setSelected] = useState<string>()
  const [switching, setSwitching] = useState(false)
  const [tab, setTab] = useState<Tab>('runs')

  const back = (
    <Button asChild variant="ghost" size="icon-sm" aria-label="Back to every workflow">
      <Link to="/admin/stats?tab=workflows">
        <ArrowLeft />
      </Link>
    </Button>
  )

  if (workflow.isError) {
    return (
      <div className="flex flex-col gap-4">
        {back}
        {workflow.error instanceof ApiError && workflow.error.status === 404 ? (
          <p className="text-sm">No such workflow, or no longer.</p>
        ) : (
          <ErrorAlert error={workflow.error} />
        )}
      </div>
    )
  }

  if (workflow.isPending || nodeTypes.isPending) {
    return <Skeleton className="h-96" />
  }

  const w = workflow.data
  const types = nodeTypes.data ?? []
  const node = w.nodes.find((n) => n.id === selected)
  const type = node && types.find((t) => t.name === node.type)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          {back}
          <h2 className="truncate font-medium">{w.name}</h2>
          {w.hold ? (
            <Badge variant="destructive">Held</Badge>
          ) : (
            w.active && <Badge variant="secondary">Active</Badge>
          )}
          {listed && (
            <span className="truncate text-sm text-muted-foreground">{listed.ownerEmail}</span>
          )}
        </div>
        {w.hold ? (
          <Button
            variant="outline"
            disabled={release.isPending}
            onClick={() =>
              release.mutate(
                { owner: uid, id },
                {
                  onSuccess: () => {
                    toast.success(`“${w.name}” released`)
                    void workflow.refetch()
                  },
                  onError: (error) => toast.error(errorMessage(error)),
                },
              )
            }
          >
            Release
          </Button>
        ) : (
          <Button variant="destructive" onClick={() => setSwitching(true)}>
            {w.active ? 'Switch off' : 'Hold off'}
          </Button>
        )}
      </div>

      {w.hold && (
        <p className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm">
          Held off by an admin since {formatDateTime(w.hold.at)}: {w.hold.reason}
        </p>
      )}

      <p className="text-xs text-muted-foreground">
        Read-only. Personal settings, such as webhook addresses, stay hidden, and so do the items
        its runs carried.
      </p>

      <div className="flex h-96 overflow-hidden rounded-lg border">
        <aside className="w-80 shrink-0 overflow-y-auto border-r p-4 text-sm" aria-label="Node">
          {node ? (
            <div className="flex flex-col gap-3">
              <div>
                <h3 className="font-medium">{node.name || type?.label || node.type}</h3>
                <p className="font-mono text-xs text-muted-foreground">{node.type}</p>
              </div>
              {node.notes && <p className="whitespace-pre-line">{node.notes}</p>}
              {type ? (
                <NodeSettings node={node} type={type} />
              ) : (
                <p className="text-muted-foreground">
                  A node type of its owner&apos;s, such as a custom node: its settings are not
                  listed here.
                </p>
              )}
            </div>
          ) : (
            <p className="text-muted-foreground">Click a node to see how it is set.</p>
          )}
        </aside>
        <div className="min-w-0 flex-1">
          <TemplateCanvas template={w} types={types} onSelect={setSelected} />
        </div>
      </div>

      <section aria-label="Runs and history" className="flex flex-col gap-2">
        <div role="tablist" aria-label="Runs and history" className="flex gap-1">
          {(['runs', 'history'] as const).map((t) => (
            <Button
              key={t}
              role="tab"
              aria-selected={tab === t}
              variant={tab === t ? 'secondary' : 'ghost'}
              size="sm"
              onClick={() => setTab(t)}
            >
              {t === 'runs' ? 'Runs' : 'History'}
            </Button>
          ))}
        </div>
        <div
          role="tabpanel"
          aria-label={tab === 'runs' ? 'Runs' : 'History'}
          className="rounded-lg border"
        >
          {tab === 'runs' ? (
            <Runs owner={uid} id={id} />
          ) : (
            <SwitchList workflowId={id} owner={uid} kinds={[...switchKinds]} />
          )}
        </div>
      </section>

      <SwitchOffDialog
        workflow={{ id, name: w.name, active: w.active, ownerEmail: listed?.ownerEmail }}
        owner={uid}
        open={switching}
        onClose={() => setSwitching(false)}
        onDone={() => void workflow.refetch()}
      />
    </div>
  )
}

function Runs({ owner, id }: { owner: string; id: string }) {
  const executions = useAdminExecutions(owner, id)

  if (executions.isError) {
    return (
      <div className="p-3">
        <ErrorAlert error={executions.error} onRetry={() => void executions.refetch()} />
      </div>
    )
  }

  if (executions.data?.length === 0) {
    return <p className="p-4 text-sm text-muted-foreground">No runs kept.</p>
  }

  return (
    <ul>
      {executions.data?.map((e) => (
        <li key={e.id} className="flex items-center gap-4 px-4 py-1.5 text-sm">
          <ExecutionStatus status={e.status} />
          <span className="w-40 tabular-nums">{formatDateTime(e.startedAt)}</span>
          <span className="w-20 text-muted-foreground tabular-nums">
            {e.finishedAt ? formatDuration(e.durationMs) : '…'}
          </span>
          <span className="w-28 truncate text-muted-foreground">{e.triggeredBy}</span>
          {e.error && <span className="min-w-0 flex-1 truncate text-destructive">{e.error}</span>}
        </li>
      ))}
    </ul>
  )
}
