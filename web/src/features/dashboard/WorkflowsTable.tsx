import { Link } from 'react-router'

import type { WorkflowStats } from '@/api/workflows'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHeader, TableRow } from '@/components/ui/table'
import { Profit } from '@/features/trading/Profit'
import { formatAgo, formatDollars } from '@/lib/format'

import { type SortValue, sortRows, useSort, winRate } from './sort'
import { SortHeader } from './SortHeader'

type Column =
  | 'name'
  | 'profit'
  | 'realized'
  | 'unrealized'
  | 'positions'
  | 'volume'
  | 'winRate'
  | 'runs'
  | 'failed'
  | 'last'

type Props = { mode: 'paper' | 'live'; workflows: WorkflowStats[] }

/**
 * Every workflow, its results in the mode shown where it trades in it, and its runs this
 * week (D429), sorted as its heads are clicked: the most profitable first to start with.
 */
export function WorkflowsTable({ mode, workflows }: Props) {
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
                      <Link
                        to={t ? `/workflows/${w.id}?view=trading` : `/workflows/${w.id}`}
                        className="truncate font-medium hover:underline"
                      >
                        {w.name}
                      </Link>
                      {w.active && <Badge variant="secondary">Active</Badge>}
                    </div>
                  </TableCell>
                  <Money text={t?.profit} profit />
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

function Money({ text, profit }: { text?: string; profit?: boolean }) {
  return (
    <TableCell className="text-right tabular-nums">
      {text === undefined ? <None /> : profit ? <Profit text={text} /> : formatDollars(text)}
    </TableCell>
  )
}

function Count({ value, suffix = '' }: { value?: number; suffix?: string }) {
  return (
    <TableCell className="text-right tabular-nums">
      {value === undefined ? <None /> : `${value}${suffix}`}
    </TableCell>
  )
}

function None() {
  return <span className="text-muted-foreground">—</span>
}
