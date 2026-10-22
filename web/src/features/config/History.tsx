import { useSettingHistory, type Setting } from '@/api/settings'
import { ErrorAlert } from '@/components/ErrorAlert'
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

export function History({ settings }: { settings: Setting[] }) {
  const history = useSettingHistory()
  const labels = new Map(settings.map((s) => [s.key, s.label]))
  const items = history.data?.pages.flatMap((page) => page.changes) ?? []

  return (
    <section className="flex flex-col gap-4">
      <h2 className="font-medium">History</h2>

      {history.isPending && <Skeleton className="h-24" />}
      {history.isError && (
        <ErrorAlert error={history.error} onRetry={() => void history.refetch()} />
      )}
      {history.isSuccess && items.length === 0 && (
        <p className="text-sm text-muted-foreground">No changes yet.</p>
      )}

      {items.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>When</TableHead>
              <TableHead>Setting</TableHead>
              <TableHead>Change</TableHead>
              <TableHead>By</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((c) => (
              <TableRow key={c.id}>
                <TableCell>{formatDateTime(c.at)}</TableCell>
                <TableCell>{labels.get(c.key) ?? c.key}</TableCell>
                <TableCell>
                  {c.secret ? (
                    'Replaced or cleared'
                  ) : (
                    <span className="flex flex-wrap items-center gap-1.5">
                      <Shown value={c.oldValue} /> → <Shown value={c.newValue} />
                    </span>
                  )}
                </TableCell>
                <TableCell>{c.actorEmail ?? 'A deleted account'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {history.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          onClick={() => void history.fetchNextPage()}
          disabled={history.isFetchingNextPage}
        >
          Load more
        </Button>
      )}
    </section>
  )
}

function Shown({ value }: { value: unknown }) {
  const text = Array.isArray(value) ? (value.length > 0 ? value.join(', ') : 'none') : String(value)

  return <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">{text}</code>
}
