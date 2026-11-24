import { Download, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { errorMessage, type Schemas } from '@/api/client'
import { datasetCSV, useDatasetRows, useDatasets, useDeleteDataset } from '@/api/datasets'
import { ConfirmDialog } from '@/components/ConfirmDialog'
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
import { formatBytes, formatDateTime } from '@/lib/format'

type Dataset = Schemas['Dataset']

/**
 * Your datasets: what notebooks publish and workflows record. Each with its rows and size,
 * a look at its first rows, a CSV of all of them, and delete.
 */
export function DatasetsPage() {
  const me = useMe()
  const list = useDatasets()
  const remove = useDeleteDataset()
  const [open, setOpen] = useState<string>()
  const [deleting, setDeleting] = useState<Dataset>()
  const datasets = list.data?.datasets ?? []

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-4 p-6">
      <div>
        <h1 className="font-heading text-xl font-semibold">Datasets</h1>
        <p className="text-sm text-muted-foreground">
          Tables of rows your notebooks publish and your workflows record, by name.{' '}
          <a className="underline" href="/docs/guides/datasets/" target="_blank" rel="noopener">
            How they work
          </a>
        </p>
      </div>

      {me.data && !me.data.mayUseData && (
        <p className="text-sm text-muted-foreground">
          Datasets are not open to you here: an admin decides who may use them.
        </p>
      )}

      {list.isPending && <Skeleton className="h-32" />}
      {list.isError && <ErrorAlert error={list.error} onRetry={() => void list.refetch()} />}

      {list.isSuccess && datasets.length === 0 && (
        <p className="text-sm text-muted-foreground">
          None yet. A notebook makes one through the API, with a token that may write datasets.
        </p>
      )}

      {datasets.length > 0 && (
        <Table aria-label="Datasets">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Key</TableHead>
              <TableHead className="text-right">Rows</TableHead>
              <TableHead className="text-right">Size</TableHead>
              <TableHead>Keeps</TableHead>
              <TableHead>Updated</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {datasets.map((d) => (
              <TableRow key={d.name}>
                <TableCell className="font-medium">
                  <Button
                    variant="link"
                    className="h-auto p-0"
                    aria-expanded={open === d.name}
                    onClick={() => setOpen(open === d.name ? undefined : d.name)}
                  >
                    {d.name}
                  </Button>
                </TableCell>
                <TableCell className="text-muted-foreground">{d.keyField || '—'}</TableCell>
                <TableCell className="text-right tabular-nums">
                  {d.rows.toLocaleString('en-US')}
                </TableCell>
                <TableCell className="text-right tabular-nums">{formatBytes(d.bytes)}</TableCell>
                <TableCell className="text-muted-foreground">{keeps(d)}</TableCell>
                <TableCell className="text-muted-foreground">
                  {formatDateTime(d.updatedAt)}
                </TableCell>
                <TableCell className="flex justify-end gap-1">
                  <Button variant="ghost" size="icon-sm" asChild>
                    <a href={datasetCSV(d.name)} download aria-label={`Download ${d.name} as CSV`}>
                      <Download />
                    </a>
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`Delete ${d.name}`}
                    onClick={() => setDeleting(d)}
                  >
                    <Trash2 />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {open && <Preview name={open} />}

      <ConfirmDialog
        open={deleting !== undefined}
        title={`Delete ${deleting?.name}?`}
        description={`Its ${deleting?.rows.toLocaleString('en-US') ?? 0} rows go with it, for good.`}
        confirm="Delete"
        destructive
        onCancel={() => setDeleting(undefined)}
        onConfirm={() => {
          const name = deleting?.name
          if (!name) return

          remove.mutate(name, {
            onSuccess: () => {
              toast.success(`Deleted ${name}`)
              if (open === name) setOpen(undefined)
            },
            onError: (error) => toast.error(errorMessage(error)),
          })
        }}
      />
    </div>
  )
}

/** What a dataset keeps: so many days, so many rows, or everything. */
function keeps(d: Dataset): string {
  const parts = [
    d.keepDays > 0 ? `${d.keepDays} days` : '',
    d.keepRows > 0 ? `${d.keepRows.toLocaleString('en-US')} rows` : '',
  ].filter(Boolean)

  return parts.length > 0 ? parts.join(', ') : 'Everything'
}

/** A dataset's first rows, a column a field. */
function Preview({ name }: { name: string }) {
  const rows = useDatasetRows(name)
  const items = rows.data?.rows ?? []
  const columns = [...new Set(items.flatMap((r) => Object.keys(r.row)))]

  return (
    <section aria-label={`${name}'s first rows`} className="flex flex-col gap-2">
      <h2 className="text-sm font-medium">{name}: its first rows</h2>
      {rows.isPending && <Skeleton className="h-24" />}
      {rows.isError && <ErrorAlert error={rows.error} onRetry={() => void rows.refetch()} />}
      {rows.isSuccess && items.length === 0 && (
        <p className="text-sm text-muted-foreground">It has no rows.</p>
      )}
      {items.length > 0 && (
        <div className="overflow-x-auto rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                {columns.map((c) => (
                  <TableHead key={c}>{c}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((r) => (
                <TableRow key={r.seq}>
                  {columns.map((c) => (
                    <TableCell key={c} className="font-mono text-xs">
                      {cell(r.row[c])}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  )
}

function cell(v: unknown): string {
  if (v === undefined || v === null) return ''
  return typeof v === 'string' ? v : JSON.stringify(v)
}
