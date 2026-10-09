import { Plus } from 'lucide-react'
import { Link } from 'react-router'

import { useWorkflows } from '@/api/workflows'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatDateTime } from '@/lib/format'

export function WorkflowsPage() {
  const workflows = useWorkflows()
  const items = workflows.data?.pages.flatMap((page) => page.workflows) ?? []

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="font-heading text-xl font-semibold">Workflows</h1>
        <Button asChild>
          <Link to="/workflows/new">
            <Plus />
            New workflow
          </Link>
        </Button>
      </div>

      {workflows.isPending && (
        <div className="flex flex-col gap-2">
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
        </div>
      )}

      {workflows.isError && (
        <ErrorAlert error={workflows.error} onRetry={() => void workflows.refetch()} />
      )}

      {workflows.isSuccess && items.length === 0 && (
        <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed p-8 text-center text-sm">
          <p className="font-medium">No workflows yet.</p>
          <p className="max-w-md text-muted-foreground">
            New here? The Library has ready-made scenarios that show what workflows do, step by
            step. Make one your own, then change it.
          </p>
          <Button asChild variant="outline">
            <Link to="/library">Browse the library</Link>
          </Button>
        </div>
      )}

      {items.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Nodes</TableHead>
              <TableHead className="text-right">Version</TableHead>
              <TableHead>Updated</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((w) => (
              <TableRow key={w.id}>
                <TableCell>
                  <Link to={`/workflows/${w.id}`} className="font-medium hover:underline">
                    {w.name}
                  </Link>
                </TableCell>
                <TableCell>
                  <Badge variant={w.active ? 'default' : 'secondary'}>
                    {w.active ? 'Active' : 'Inactive'}
                  </Badge>
                </TableCell>
                <TableCell className="text-right tabular-nums">{w.nodes.length}</TableCell>
                <TableCell className="text-right tabular-nums">{w.version}</TableCell>
                <TableCell className="text-muted-foreground">
                  {formatDateTime(w.updatedAt)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {workflows.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          onClick={() => void workflows.fetchNextPage()}
          disabled={workflows.isFetchingNextPage}
        >
          Load more
        </Button>
      )}
    </div>
  )
}
