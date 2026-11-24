import { useSearchParams } from 'react-router'

import { useAdminWorkflows, useInstanceStats } from '@/api/admin'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { AdminWorkflowsTable } from '@/features/admin/AdminWorkflowsTable'
import { useMode } from '@/features/dashboard/mode'
import { ModeToggle } from '@/features/dashboard/ModeToggle'
import { Totals } from '@/features/dashboard/Totals'
import { UsersTable } from '@/features/dashboard/UsersTable'
import { formatDateTime } from '@/lib/format'

type Tab = 'users' | 'workflows'

const tabNames: Record<Tab, string> = { users: 'Users', workflows: 'Workflows' }

/**
 * Admin → Stats: how every user's workflows are doing (D431), added up, on paper or live,
 * each user's opening their dashboard; and every workflow, the busiest first, each opened,
 * switched off or held off (D435). The tab is in the address.
 */
export function AdminStatsPage() {
  const [params, setParams] = useSearchParams()
  const tab: Tab = params.get('tab') === 'workflows' ? 'workflows' : 'users'

  const show = (t: Tab) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        next.set('tab', t)
        return next
      },
      { replace: true },
    )

  return (
    <div className="flex flex-col gap-6">
      <div role="tablist" aria-label="Stats" className="flex gap-1">
        {(Object.keys(tabNames) as Tab[]).map((t) => (
          <Button
            key={t}
            role="tab"
            aria-selected={tab === t}
            variant={tab === t ? 'secondary' : 'ghost'}
            size="sm"
            onClick={() => show(t)}
          >
            {tabNames[t]}
          </Button>
        ))}
      </div>

      {tab === 'users' ? <UsersTab /> : <WorkflowsTab />}
    </div>
  )
}

function UsersTab() {
  const stats = useInstanceStats()
  const s = stats.data
  const [mode, setMode] = useMode(!!s && s.live.workflows > 0)

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted-foreground">
          Every user&apos;s workflows together, as their dashboards have them.
        </p>
        <ModeToggle mode={mode} onChange={setMode} />
      </div>

      {stats.isPending && <Skeleton className="h-64" />}
      {stats.isError && <ErrorAlert error={stats.error} onRetry={() => void stats.refetch()} />}

      {s && (
        <>
          <Totals mode={mode} totals={s[mode]} day={s.day} week={s.week} />
          <UsersTable mode={mode} users={s.users} />
          <p className="text-xs text-muted-foreground">
            As of {formatDateTime(s.at)}, asked again each minute. Users without workflows are not
            listed.
          </p>
        </>
      )}
    </div>
  )
}

function WorkflowsTab() {
  const workflows = useAdminWorkflows()
  const w = workflows.data

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        Every user&apos;s workflows, the busiest in the last hour first. One running far more than
        it should can be switched off, and held off until an admin releases it.
      </p>

      {workflows.isPending && <Skeleton className="h-64" />}
      {workflows.isError && (
        <ErrorAlert error={workflows.error} onRetry={() => void workflows.refetch()} />
      )}

      {w && (
        <>
          <AdminWorkflowsTable workflows={w.workflows} />
          <p className="text-xs text-muted-foreground">
            As of {formatDateTime(w.at)}, asked again each minute.
          </p>
        </>
      )}
    </div>
  )
}
