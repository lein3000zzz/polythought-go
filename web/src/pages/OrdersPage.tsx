import { Download } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'

import type { Schemas } from '@/api/client'
import { exportURL, type OrderLogFilter, useLiveOrders, useOrderActors } from '@/api/orders'
import { useWallets } from '@/api/wallets'
import { useWorkflows } from '@/api/workflows'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { orderText, resultText, traderName } from '@/features/trading/orderText'
import { actorName, actorSays } from '@/lib/actors'
import { formatDateTime, shortAddress } from '@/lib/format'

type Status = NonNullable<OrderLogFilter['status']>

const statuses: Status[] = [
  'filled',
  'partial',
  'open',
  'canceled',
  'expired',
  'unfilled',
  'refused',
  'pending',
  'failed',
]

/**
 * Every live order of your workflows, deleted ones' too (D228, D199): the record of the
 * real money they spent, narrowed by wallet, workflow, status and who is behind them (D408),
 * and downloaded as CSV. The filters live in the address, so a wallet links to its own.
 */
export function OrdersPage() {
  const [params, setParams] = useSearchParams()
  const filter: OrderLogFilter = {
    wallet: params.get('wallet') || undefined,
    workflow: params.get('workflow') || undefined,
    status: (params.get('status') as Status | null) || undefined,
    by: params.get('by') || undefined,
  }

  const orders = useLiveOrders(filter)
  const wallets = useWallets()
  const workflows = useWorkflows()
  const actors = useOrderActors()
  const items = orders.data?.pages.flatMap((p) => p.orders) ?? []
  const walletName = new Map(wallets.data?.map((w) => [w.id, w.label || shortAddress(w.address)]))

  const narrow = (key: keyof OrderLogFilter, value: string) =>
    setParams((p) => {
      const next = new URLSearchParams(p)
      if (value) next.set(key, value)
      else next.delete(key)
      return next
    })

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h1 className="font-heading text-xl font-semibold">Live orders</h1>
          <p className="text-sm text-muted-foreground">
            Every live order of your workflows, deleted ones&apos; too: the real money they spent.
          </p>
        </div>
        <Button variant="outline" asChild>
          <a href={exportURL(filter)} download>
            <Download />
            Download CSV
          </a>
        </Button>
      </div>

      <div className="flex flex-wrap gap-4">
        <Filter
          label="Wallet"
          id="orders-wallet"
          value={filter.wallet}
          onChange={(v) => narrow('wallet', v)}
        >
          <NativeSelectOption value="">All wallets</NativeSelectOption>
          {wallets.data?.map((w) => (
            <NativeSelectOption key={w.id} value={w.id}>
              {w.label || shortAddress(w.address)}
            </NativeSelectOption>
          ))}
        </Filter>
        <Filter
          label="Workflow"
          id="orders-workflow"
          value={filter.workflow}
          onChange={(v) => narrow('workflow', v)}
        >
          <NativeSelectOption value="">All workflows</NativeSelectOption>
          {workflows.data?.pages
            .flatMap((p) => p.workflows)
            .map((w) => (
              <NativeSelectOption key={w.id} value={w.id}>
                {w.name}
              </NativeSelectOption>
            ))}
        </Filter>
        <Filter
          label="Status"
          id="orders-status"
          value={filter.status}
          onChange={(v) => narrow('status', v)}
        >
          <NativeSelectOption value="">Any status</NativeSelectOption>
          {statuses.map((s) => (
            <NativeSelectOption key={s} value={s}>
              {s}
            </NativeSelectOption>
          ))}
        </Filter>
        <Filter label="By" id="orders-by" value={filter.by} onChange={(v) => narrow('by', v)}>
          <NativeSelectOption value="">Everyone</NativeSelectOption>
          <NativeSelectOption value="you">You</NativeSelectOption>
          {actors.data?.map((a) => (
            <NativeSelectOption key={`${a.kind}:${a.id}`} value={`${a.kind}:${a.id}`}>
              {actorName(a)}
            </NativeSelectOption>
          ))}
        </Filter>
      </div>

      {orders.isPending && <Skeleton className="h-48" />}
      {orders.isError && <ErrorAlert error={orders.error} onRetry={() => void orders.refetch()} />}
      {orders.isSuccess && items.length === 0 && (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
          No live orders
          {filter.wallet || filter.workflow || filter.status || filter.by ? ' like that' : ' yet'}.
        </p>
      )}

      {items.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>At</TableHead>
              <TableHead>Workflow</TableHead>
              <TableHead>Wallet</TableHead>
              <TableHead>Order</TableHead>
              <TableHead>Market</TableHead>
              <TableHead>Result</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((o) => (
              <TableRow key={o.id}>
                <TableCell className="whitespace-nowrap">{formatDateTime(o.at)}</TableCell>
                <TableCell>
                  <WorkflowName order={o} />
                  {o.by && (
                    <span className="block text-xs text-muted-foreground">{actorSays(o.by)}</span>
                  )}
                </TableCell>
                <TableCell>
                  {o.walletId ? (walletName.get(o.walletId) ?? 'An unlinked wallet') : '—'}
                </TableCell>
                <TableCell className="whitespace-nowrap">
                  {orderText(o)}
                  {o.trader && (
                    <span className="block text-xs text-muted-foreground">
                      copying {traderName(o.traderLabel, o.trader)}
                    </span>
                  )}
                </TableCell>
                <TableCell className="max-w-64 truncate" title={market(o)}>
                  {market(o)}
                </TableCell>
                <TableCell>{resultText(o)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {orders.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          onClick={() => void orders.fetchNextPage()}
          disabled={orders.isFetchingNextPage}
        >
          Load more
        </Button>
      )}
    </div>
  )
}

function Filter({
  label,
  id,
  value,
  onChange,
  children,
}: {
  label: string
  id: string
  value?: string
  onChange: (value: string) => void
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <NativeSelect id={id} value={value ?? ''} onChange={(e) => onChange(e.target.value)}>
        {children}
      </NativeSelect>
    </div>
  )
}

function WorkflowName({ order: o }: { order: Schemas['LoggedOrder'] }) {
  if (o.deleted) return <span className="text-muted-foreground">{o.workflow} (deleted)</span>

  return (
    <Link to={`/workflows/${o.workflowId}`} className="underline-offset-4 hover:underline">
      {o.workflow}
    </Link>
  )
}

function market(o: Schemas['LoggedOrder']): string {
  if (!o.question) return shortAddress(o.tokenId)

  return o.outcome ? `${o.question} · ${o.outcome}` : o.question
}
