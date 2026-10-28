import { ArrowLeft, SlidersHorizontal } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'
import { toast } from 'sonner'

import { useMe, useSetDashboard } from '@/api/auth'
import { errorMessage } from '@/api/client'
import { useWorkflowStats } from '@/api/workflows'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Skeleton } from '@/components/ui/skeleton'
import { Totals } from '@/features/dashboard/Totals'
import { TradersTable } from '@/features/dashboard/TradersTable'
import { WorkflowsTable } from '@/features/dashboard/WorkflowsTable'
import { formatDateTime } from '@/lib/format'

type Mode = 'paper' | 'live'

/** The dashboard's parts, by the names the hidden ones are kept under (D429). */
const parts = [
  { name: 'totals', label: 'Totals' },
  { name: 'workflows', label: 'Workflows' },
  { name: 'traders', label: 'Traders' },
] as const

const modeLabels: Record<Mode, string> = { paper: 'Paper', live: 'Live' }

/**
 * How all your workflows are doing (D429), from the Workflows page: totals, every workflow,
 * and the traders they copy, on paper or live, each part shown or hidden as you choose,
 * kept with your account.
 */
export function DashboardPage() {
  const stats = useWorkflowStats()
  const me = useMe()
  const setDashboard = useSetDashboard()
  const [params, setParams] = useSearchParams()

  const s = stats.data
  const asked = params.get('mode')
  // Live where anything trades live, unless the page says otherwise.
  const mode: Mode =
    asked === 'paper' || asked === 'live' ? asked : s && s.live.workflows > 0 ? 'live' : 'paper'
  const hidden = me.data?.dashboard?.hidden ?? []
  const shows = (part: string) => !hidden.includes(part)

  const toggle = (part: string, show: boolean) =>
    setDashboard.mutate(show ? hidden.filter((h) => h !== part) : [...hidden, part], {
      onError: (error) => toast.error(errorMessage(error)),
    })

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Button asChild variant="ghost" size="icon-sm" aria-label="Back to workflows">
            <Link to="/workflows">
              <ArrowLeft />
            </Link>
          </Button>
          <h1 className="font-heading text-xl font-semibold">Dashboard</h1>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div role="group" aria-label="Mode" className="flex gap-1">
            {(Object.keys(modeLabels) as Mode[]).map((m) => (
              <Button
                key={m}
                size="sm"
                variant={mode === m ? 'secondary' : 'ghost'}
                aria-pressed={mode === m}
                onClick={() => setParams({ mode: m }, { replace: true })}
              >
                {modeLabels[m]}
              </Button>
            ))}
          </div>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <SlidersHorizontal />
                Customize
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel>Show</DropdownMenuLabel>
              {parts.map((p) => (
                <DropdownMenuCheckboxItem
                  key={p.name}
                  checked={shows(p.name)}
                  onCheckedChange={(show) => toggle(p.name, show)}
                  onSelect={(e) => e.preventDefault()} // the menu stays open for the next
                >
                  {p.label}
                </DropdownMenuCheckboxItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      {stats.isPending && (
        <div className="flex flex-col gap-4">
          <Skeleton className="h-40" />
          <Skeleton className="h-64" />
        </div>
      )}
      {stats.isError && <ErrorAlert error={stats.error} onRetry={() => void stats.refetch()} />}

      {s && (
        <>
          {shows('totals') && <Totals mode={mode} totals={s[mode]} day={s.day} week={s.week} />}
          {shows('workflows') && <WorkflowsTable mode={mode} workflows={s.workflows} />}
          {shows('traders') && <TradersTable mode={mode} traders={s.traders} />}

          {parts.every((p) => !shows(p.name)) && (
            <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
              Every part is hidden: Customize shows them again.
            </p>
          )}

          <p className="text-xs text-muted-foreground">
            As of {formatDateTime(s.at)}, asked again each minute. Prices of positions held are the
            market&apos;s now.
          </p>
        </>
      )}
    </div>
  )
}
