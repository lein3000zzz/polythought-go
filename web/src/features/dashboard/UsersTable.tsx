import { Link } from 'react-router'

import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHeader, TableRow } from '@/components/ui/table'

import { Count, Money } from './cells'
import type { Mode } from './mode'
import { type SortValue, sortRows, useSort, winRate } from './sort'
import { SortHeader } from './SortHeader'

type User = Schemas['UserTotals']
type Column =
  | 'email'
  | 'workflows'
  | 'active'
  | 'profit'
  | 'realized'
  | 'unrealized'
  | 'positions'
  | 'volume'
  | 'winRate'
  | 'runs'
  | 'failed'

/**
 * Every user with workflows, theirs added up in the mode shown (D431), each opening their
 * dashboard: the most profitable first to start with.
 */
export function UsersTable({ mode, users }: { mode: Mode; users: User[] }) {
  const [sort, by] = useSort<Column>({ key: 'profit', dir: 'desc' }, ['email'])

  const value = (u: User, key: Column): SortValue => {
    const t = u[mode].workflows > 0 ? u[mode] : undefined

    switch (key) {
      case 'email':
        return u.email.toLowerCase()
      case 'workflows':
        return u[mode].workflows
      case 'active':
        return u.active
      case 'runs':
        return u.week.runs
      case 'failed':
        return u.week.failed
      case 'winRate':
        return t && winRate(t.wins, t.closed)
      case 'positions':
        return t?.positions
      default:
        return t && Number(t[key])
    }
  }

  const head = { sort, onSort: by }

  return (
    <section aria-labelledby="stats-users" className="flex flex-col gap-3">
      <h2 id="stats-users" className="font-medium">
        Users
      </h2>
      {users.length === 0 ? (
        <p className="text-sm text-muted-foreground">No one has workflows yet.</p>
      ) : (
        <Table aria-labelledby="stats-users">
          <TableHeader>
            <TableRow>
              <SortHeader label="User" column="email" {...head} />
              <SortHeader label="Workflows" column="workflows" numeric {...head} />
              <SortHeader label="Active" column="active" numeric {...head} />
              <SortHeader label="Profit" column="profit" numeric {...head} />
              <SortHeader label="Realized" column="realized" numeric {...head} />
              <SortHeader label="Unrealized" column="unrealized" numeric {...head} />
              <SortHeader label="Positions" column="positions" numeric {...head} />
              <SortHeader label="Volume" column="volume" numeric {...head} />
              <SortHeader label="Win rate" column="winRate" numeric {...head} />
              <SortHeader label="Runs, week" column="runs" numeric {...head} />
              <SortHeader label="Failed" column="failed" numeric {...head} />
            </TableRow>
          </TableHeader>
          <TableBody>
            {sortRows(users, sort, value).map((u) => {
              const t = u[mode].workflows > 0 ? u[mode] : undefined
              const rate = t && winRate(t.wins, t.closed)

              return (
                <TableRow key={u.id}>
                  <TableCell className="max-w-64">
                    <div className="flex items-center gap-2">
                      <Link
                        to={`/admin/stats/${u.id}?mode=${mode}`}
                        className="truncate font-medium hover:underline"
                      >
                        {u.email}
                      </Link>
                      {u.role === 'admin' && <Badge variant="secondary">Admin</Badge>}
                      {u.disabled && <Badge variant="outline">Disabled</Badge>}
                    </div>
                  </TableCell>
                  <TableCell
                    className="text-right whitespace-nowrap tabular-nums"
                    title={`${u[mode].workflows} trading ${mode === 'live' ? 'live' : 'on paper'}, of ${u.workflows}`}
                  >
                    {u[mode].workflows}
                    <span className="text-muted-foreground"> of {u.workflows}</span>
                  </TableCell>
                  <Count value={u.active} />
                  <Money text={t?.profit} profit />
                  <Money text={t?.realized} profit />
                  <Money text={t?.unrealized} profit />
                  <Count value={t?.positions} />
                  <Money text={t?.volume} />
                  <Count value={rate} suffix="%" />
                  <Count value={u.week.runs} />
                  <TableCell
                    className={
                      u.week.failed > 0
                        ? 'text-right text-destructive tabular-nums'
                        : 'text-right tabular-nums'
                    }
                  >
                    {u.week.failed}
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
