import { useState } from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { useAdminRelease } from '@/api/admin'
import { errorMessage, type Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { type SortValue, sortRows, useSort } from '@/features/dashboard/sort'
import { SortHeader } from '@/features/dashboard/SortHeader'
import { formatAgo, formatDateTime } from '@/lib/format'

import { SwitchOffDialog } from './SwitchOffDialog'

type Workflow = Schemas['AdminWorkflow']
type Column = 'name' | 'owner' | 'hour' | 'day' | 'week' | 'failed' | 'last'

/**
 * Every user's workflows (D435), the busiest in the last hour first, sorted as their
 * heads are clicked: each opened read-only, switched off with a reason, or released.
 */
export function AdminWorkflowsTable({ workflows }: { workflows: Workflow[] }) {
  const [sort, by] = useSort<Column>({ key: 'hour', dir: 'desc' }, ['name', 'owner'])
  const [switching, setSwitching] = useState<Workflow>()
  const release = useAdminRelease()

  const value = (w: Workflow, key: Column): SortValue => {
    switch (key) {
      case 'name':
        return w.name.toLowerCase()
      case 'owner':
        return w.ownerEmail.toLowerCase()
      case 'failed':
        return w.hour.failed
      case 'last':
        return w.last ? Date.parse(w.last.startedAt) : undefined
      default:
        return w[key].runs
    }
  }

  const head = { sort, onSort: by }

  if (workflows.length === 0) {
    return <p className="text-sm text-muted-foreground">No workflows yet.</p>
  }

  return (
    <>
      <Table aria-label="Every user's workflows">
        <TableHeader>
          <TableRow>
            <SortHeader label="Workflow" column="name" {...head} />
            <SortHeader label="Owner" column="owner" {...head} />
            <SortHeader label="Runs, hour" column="hour" numeric {...head} />
            <SortHeader label="Failed, hour" column="failed" numeric {...head} />
            <SortHeader label="Runs, day" column="day" numeric {...head} />
            <SortHeader label="Runs, week" column="week" numeric {...head} />
            <SortHeader label="Last run" column="last" {...head} />
            <TableHead>Triggers</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {sortRows(workflows, sort, value).map((w) => (
            <TableRow key={w.id}>
              <TableCell className="max-w-64">
                <div className="flex items-center gap-2">
                  <Link
                    to={`/admin/users/${w.ownerId}/workflows/${w.id}`}
                    className="truncate font-medium hover:underline"
                  >
                    {w.name}
                  </Link>
                  {w.hold ? (
                    <Badge variant="destructive" title={`Held by an admin: ${w.hold.reason}`}>
                      Held
                    </Badge>
                  ) : (
                    w.active && <Badge variant="secondary">Active</Badge>
                  )}
                </div>
              </TableCell>
              <TableCell className="max-w-48 truncate text-muted-foreground">
                {w.ownerEmail}
              </TableCell>
              <TableCell className="text-right font-medium tabular-nums">{w.hour.runs}</TableCell>
              <TableCell
                className={
                  w.hour.failed > 0
                    ? 'text-right text-destructive tabular-nums'
                    : 'text-right tabular-nums'
                }
              >
                {w.hour.failed}
              </TableCell>
              <TableCell className="text-right tabular-nums">{w.day.runs}</TableCell>
              <TableCell className="text-right tabular-nums">{w.week.runs}</TableCell>
              <TableCell
                className="whitespace-nowrap text-muted-foreground"
                title={w.last && formatDateTime(w.last.startedAt)}
              >
                {w.last ? formatAgo(w.last.startedAt) : 'never'}
              </TableCell>
              <TableCell className="max-w-48 truncate font-mono text-xs text-muted-foreground">
                {w.triggers.join(', ') || '—'}
              </TableCell>
              <TableCell className="text-right">
                {w.hold ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={release.isPending}
                    onClick={() =>
                      release.mutate(
                        { owner: w.ownerId, id: w.id },
                        {
                          onSuccess: () => toast.success(`“${w.name}” released`),
                          onError: (error) => toast.error(errorMessage(error)),
                        },
                      )
                    }
                  >
                    Release
                  </Button>
                ) : (
                  <Button variant="ghost" size="sm" onClick={() => setSwitching(w)}>
                    {w.active ? 'Switch off' : 'Hold off'}
                  </Button>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {switching && (
        <SwitchOffDialog
          workflow={switching}
          owner={switching.ownerId}
          open
          onClose={() => setSwitching(undefined)}
        />
      )}
    </>
  )
}
