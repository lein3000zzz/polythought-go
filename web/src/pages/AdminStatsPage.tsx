import { useInstanceStats } from '@/api/admin'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Skeleton } from '@/components/ui/skeleton'
import { useMode } from '@/features/dashboard/mode'
import { ModeToggle } from '@/features/dashboard/ModeToggle'
import { Totals } from '@/features/dashboard/Totals'
import { UsersTable } from '@/features/dashboard/UsersTable'
import { formatDateTime } from '@/lib/format'

/**
 * Admin → Stats (D431): how every user's workflows are doing, added up, on paper or live,
 * and each user's, opening their dashboard.
 */
export function AdminStatsPage() {
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
