import { ArrowLeft } from 'lucide-react'
import { Link, useParams } from 'react-router'

import { useUserStats } from '@/api/admin'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useMode } from '@/features/dashboard/mode'
import { ModeToggle } from '@/features/dashboard/ModeToggle'
import { Totals } from '@/features/dashboard/Totals'
import { TradersTable } from '@/features/dashboard/TradersTable'
import { WorkflowsTable } from '@/features/dashboard/WorkflowsTable'
import { formatDateTime } from '@/lib/format'

/**
 * One user's dashboard, for an admin (D431): every part, read-only, their workflows not
 * opened from here.
 */
export function AdminUserStatsPage() {
  const { id = '' } = useParams()
  const stats = useUserStats(id)
  const s = stats.data?.stats
  const [mode, setMode] = useMode(!!s && s.live.workflows > 0)

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <Button asChild variant="ghost" size="icon-sm" aria-label="Back to stats">
            <Link to={`/admin/stats?mode=${mode}`}>
              <ArrowLeft />
            </Link>
          </Button>
          <h2 className="truncate font-medium">{stats.data?.email ?? 'A user'}&apos;s workflows</h2>
        </div>
        <ModeToggle mode={mode} onChange={setMode} />
      </div>

      {stats.isPending && <Skeleton className="h-64" />}
      {stats.isError && <ErrorAlert error={stats.error} onRetry={() => void stats.refetch()} />}

      {s && (
        <>
          <Totals mode={mode} totals={s[mode]} day={s.day} week={s.week} />
          <WorkflowsTable mode={mode} workflows={s.workflows} linked={false} />
          <TradersTable mode={mode} traders={s.traders} />
          <p className="text-xs text-muted-foreground">
            As of {formatDateTime(s.at)}, asked again each minute.
          </p>
        </>
      )}
    </div>
  )
}
