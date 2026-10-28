import { Link } from 'react-router'

import type { Schemas } from '@/api/client'
import type { WorkflowStats } from '@/api/workflows'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHeader, TableRow } from '@/components/ui/table'
import { Profit } from '@/features/trading/Profit'
import { formatAgo, formatDateTime } from '@/lib/format'

import { Count, Money, None } from './cells'
import { type SortValue, sortRows, useSort, winRate } from './sort'
import { SortHeader } from './SortHeader'

type Column =
  | 'name'
  | 'profit'
  | 'high'
  | 'low'
  | 'realized'
  | 'unrealized'
  | 'positions'
  | 'volume'
  | 'winRate'
  | 'runs'
  | 'failed'
  | 'last'

// linked: each name opens its workflow; not for another user's, which an admin sees (D431).
type Props = { mode: 'paper' | 'live'; workflows: WorkflowStats[]; linked?: boolean }

/**
 * Every workflow, its results in the mode shown where it trades in it, its highest and
 * lowest among them (D430), and its runs this week (D429), sorted as its heads are clicked:
 * the most profitable first to start with.
 */
export function WorkflowsTable({ mode, workflows, linked = true }: Props) {
  const [sort, by] = useSort<Column>({ key: 'profit', dir: 'desc' }, ['name'])

  const value = (w: WorkflowStats, key: Column): SortValue => {
    const t = w.trading?.mode === mode ? w.trading : undefined

    switch (key) {
      case 'name':
        return w.name
      case 'runs':
        return w.runs.week.runs
      case 'failed':
        return w.runs.week.failed
      case 'last':
        return w.runs.last ? Date.parse(w.runs.last.startedAt) : undefined
      case 'winRate':
        return t && winRate(t.wins, t.closed)
      case 'positions':
        return t?.positions
      case 'high':
      case 'low':
        return t && w[key] && Number(w[key].profit)
      default:
        return t && Number(t[key])
    }
  }

  const rows = sortRows(workflows, sort, value)
  const head = { sort, onSort: by }

  return (
    <section aria-labelledby="dashboard-workflows" className="flex flex-col gap-3">
      <h2 id="dashboard-workflows" className="font-medium">
        Workflows
      </h2>
      {workflows.length === 0 ? (
        <p className="text-sm text-muted-foreground">No workflows yet.</p>
      ) : (
        <Table aria-labelledby="dashboard-workflows">
          <TableHeader>
            <TableRow>
              <SortHeader label="Workflow" column="name" {...head} />
              <SortHeader label="Profit" column="profit" numeric {...head} />
              <SortHeader label="Highest" column="high" numeric {...head} />
              <SortHeader label="Lowest" column="low" numeric {...head} />
              <SortHeader label="Realized" column="realized" numeric {...head} />
              <SortHeader label="Unrealized" column="unrealized" numeric {...head} />
              <SortHeader label="Positions" column="positions" numeric {...head} />
              <SortHeader label="Volume" column="volume" numeric {...head} />
              <SortHeader label="Win rate" column="winRate" numeric {...head} />
              <SortHeader label="Runs, week" column="runs" numeric {...head} />
              <SortHeader label="Failed" column="failed" numeric {...head} />
              <SortHeader label="Last run" column="last" {...head} />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((w) => {
              const t = w.trading?.mode === mode ? w.trading : undefined
              const rate = t && winRate(t.wins, t.closed)

              return (
                <TableRow key={w.id}>
                  <TableCell className="max-w-64">
                    <div className="flex items-center gap-2">
                      {linked ? (
                        <Link
                          to={t ? `/workflows/${w.id}?view=trading` : `/workflows/${w.id}`}
                          className="truncate font-medium hover:underline"
                        >
                          {w.name}
                        </Link>
                      ) : (
                        <span className="truncate font-medium">{w.name}</span>
                      )}
                      {w.active && <Badge variant="secondary">Active</Badge>}
                    </div>
                  </TableCell>
                  <Money text={t?.profit} profit />
                  <Extreme value={t && w.high} />
                  <Extreme value={t && w.low} />
                  <Money text={t?.realized} profit />
                  <Money text={t?.unrealized} profit />
                  <Count value={t?.positions} />
                  <Money text={t?.volume} />
                  <Count value={rate} suffix="%" />
                  <Count value={w.runs.week.runs} />
                  <TableCell
                    className={
                      w.runs.week.failed > 0
                        ? 'text-right text-destructive tabular-nums'
                        : 'text-right tabular-nums'
                    }
                  >
                    {w.runs.week.failed}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {w.runs.last ? (
                      <span
                        className={w.runs.last.status === 'failed' ? 'text-destructive' : undefined}
                      >
                        {w.runs.last.status === 'failed' ? 'failed ' : ''}
                        {formatAgo(w.runs.last.finishedAt ?? w.runs.last.startedAt)}
                      </span>
                    ) : (
                      'never'
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
    </section>
  )
}

/** A highest or a lowest profit (D430), and when it was. */
function Extreme({ value }: { value?: Schemas['PnLExtreme'] }) {
  return (
    <TableCell className="text-right tabular-nums" title={value && formatDateTime(value.at)}>
      {value ? (
        <div className="flex flex-col items-end">
          <Profit text={value.profit} />
          <span className="text-xs whitespace-nowrap text-muted-foreground">
            {formatAgo(value.at)}
          </span>
        </div>
      ) : (
        <None />
      )}
    </TableCell>
  )
}
