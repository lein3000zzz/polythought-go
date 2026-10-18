import { cn } from 'cn'
import { RotateCcw } from 'lucide-react'
import { Fragment, useState } from 'react'
import { toast } from 'sonner'

import { errorMessage, type Schemas } from '@/api/client'
import { useOrders, useResetPaperAccount, useTradingSummary } from '@/api/trading'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  formatDateTime,
  formatDollars,
  formatProfit,
  formatShares,
  shortAddress,
} from '@/lib/format'

type Summary = Schemas['TradingSummary']
type Order = Schemas['Order']

/**
 * How a workflow's trading went (D104): its paper account, each trader it copies, what
 * it holds marked to market, and its orders. Refreshed while it shows.
 */
export function TradingView({ workflowId }: { workflowId: string }) {
  const summary = useTradingSummary(workflowId)
  const orders = useOrders(workflowId)

  if (summary.isPending) return <p className="p-6 text-sm text-muted-foreground">Loading…</p>
  if (summary.isError)
    return <p className="p-6 text-sm text-destructive">{errorMessage(summary.error)}</p>

  const s = summary.data
  const names = questions(s)

  return (
    <div className="flex h-full flex-col gap-8 overflow-y-auto p-6">
      <Account workflowId={workflowId} account={s.account} />

      <section aria-labelledby="traders-heading" className="flex flex-col gap-2">
        <h2 id="traders-heading" className="font-medium">
          Traders
        </h2>
        {s.traders.length === 0 ? (
          <p className="text-sm text-muted-foreground">No copies yet.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Trader</TableHead>
                <TableHead className="text-right">Copies</TableHead>
                <TableHead className="text-right">Volume</TableHead>
                <TableHead className="text-right">Realized</TableHead>
                <TableHead className="text-right">Unrealized</TableHead>
                <TableHead className="text-right">Win rate</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {s.traders.map((t) => (
                <TableRow key={t.trader ?? ''}>
                  <TableCell>{traderName(t.label, t.trader)}</TableCell>
                  <TableCell className="text-right">{t.copies}</TableCell>
                  <TableCell className="text-right">{formatDollars(t.volume)}</TableCell>
                  <TableCell className="text-right">
                    <Profit text={t.realized} />
                  </TableCell>
                  <TableCell className="text-right">
                    <Profit text={t.unrealized} />
                  </TableCell>
                  <TableCell
                    className="text-right"
                    title="Closed copies that made money: sold out of, or settled"
                  >
                    {t.closed === 0 ? '—' : `${t.wins} of ${t.closed}`}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </section>

      <section aria-labelledby="holdings-heading" className="flex flex-col gap-2">
        <h2 id="holdings-heading" className="font-medium">
          Holdings
        </h2>
        {s.holdings.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing held.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Trader</TableHead>
                <TableHead>Market</TableHead>
                <TableHead>Outcome</TableHead>
                <TableHead className="text-right">Shares</TableHead>
                <TableHead className="text-right">Cost</TableHead>
                <TableHead className="text-right">Now</TableHead>
                <TableHead className="text-right">Worth</TableHead>
                <TableHead className="text-right">Unrealized</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {s.holdings.map((h) => (
                <TableRow key={`${h.trader ?? ''}-${h.tokenId}`}>
                  <TableCell>{traderName(h.traderLabel, h.trader)}</TableCell>
                  <TableCell className="max-w-72 truncate" title={h.question}>
                    {h.question || shortAddress(h.tokenId)}
                  </TableCell>
                  <TableCell>{h.outcome ?? '—'}</TableCell>
                  <TableCell className="text-right">{formatShares(h.shares)}</TableCell>
                  <TableCell className="text-right">{formatDollars(h.cost)}</TableCell>
                  <TableCell className="text-right">{h.mark ?? '—'}</TableCell>
                  <TableCell className="text-right">{formatDollars(h.value)}</TableCell>
                  <TableCell className="text-right">
                    <Profit text={h.unrealized} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </section>

      <section aria-labelledby="orders-heading" className="flex flex-col gap-2">
        <h2 id="orders-heading" className="font-medium">
          Orders
        </h2>
        <Orders
          orders={orders.data?.pages.flatMap((p) => p.orders) ?? []}
          resetAt={s.account.resetAt}
          names={names}
        />
        {orders.hasNextPage && (
          <Button
            variant="outline"
            size="sm"
            className="self-start"
            disabled={orders.isFetchingNextPage}
            onClick={() => void orders.fetchNextPage()}
          >
            Older orders
          </Button>
        )}
      </section>
    </div>
  )
}

function Account({ workflowId, account }: { workflowId: string; account: Summary['account'] }) {
  const reset = useResetPaperAccount(workflowId)
  const [asking, setAsking] = useState(false)

  return (
    <section aria-label="Paper account" className="flex flex-wrap items-end gap-x-8 gap-y-3">
      <Figure label="Worth" value={formatDollars(account.worth)} />
      <Figure label="Cash" value={formatDollars(account.cash)} />
      <Figure label="Profit" value={<Profit text={account.profit} />} />
      <p className="text-xs text-muted-foreground">
        {!account.open
          ? `A paper account opens with the first order, with ${formatDollars(account.startedWith)}.`
          : account.resetAt
            ? `Started over with ${formatDollars(account.startedWith)} on ${formatDateTime(account.resetAt)}.`
            : `Started with ${formatDollars(account.startedWith)} on ${formatDateTime(account.startedAt!)}.`}
      </p>
      {account.open && (
        <Button variant="outline" size="sm" className="ml-auto" onClick={() => setAsking(true)}>
          <RotateCcw />
          Reset
        </Button>
      )}

      <ConfirmDialog
        open={asking}
        title="Start the paper account over?"
        description="Its cash goes back to the Paper balance and its holdings are emptied. The orders stay, listed under the reset, and profit and loss counts from it."
        confirm="Reset"
        destructive
        onCancel={() => setAsking(false)}
        onConfirm={() =>
          reset.mutate(undefined, {
            onSuccess: () => toast.success('Paper account reset'),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }
      />
    </section>
  )
}

function Figure({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex flex-col">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-xl font-medium tabular-nums">{value}</span>
    </div>
  )
}

function Profit({ text }: { text: string }) {
  const n = Number(text)

  return (
    <span
      className={cn(
        'tabular-nums',
        n > 0 && 'text-emerald-600 dark:text-emerald-400',
        n < 0 && 'text-destructive',
      )}
    >
      {formatProfit(text)}
    </span>
  )
}

function Orders({
  orders,
  resetAt,
  names,
}: {
  orders: Order[]
  resetAt?: string
  names: Map<string, string>
}) {
  if (orders.length === 0) return <p className="text-sm text-muted-foreground">No orders yet.</p>

  const before = (o: Order) => resetAt !== undefined && o.at < resetAt
  const firstBefore = orders.findIndex(before)

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>At</TableHead>
          <TableHead>Trader</TableHead>
          <TableHead>Order</TableHead>
          <TableHead>Market</TableHead>
          <TableHead>Result</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {orders.map((o, i) => (
          <Fragment key={o.id}>
            {i === firstBefore && (
              <TableRow>
                <TableCell colSpan={5} className="text-xs text-muted-foreground">
                  Before the reset on {formatDateTime(resetAt!)}
                </TableCell>
              </TableRow>
            )}
            <TableRow className={cn(before(o) && 'text-muted-foreground')}>
              <TableCell className="whitespace-nowrap">{formatDateTime(o.at)}</TableCell>
              <TableCell>{traderName(o.traderLabel, o.trader)}</TableCell>
              <TableCell className="whitespace-nowrap">{orderText(o)}</TableCell>
              <TableCell className="max-w-64 truncate" title={names.get(o.tokenId)}>
                {names.get(o.tokenId) ?? shortAddress(o.tokenId)}
              </TableCell>
              <TableCell>{resultText(o)}</TableCell>
            </TableRow>
          </Fragment>
        ))}
      </TableBody>
    </Table>
  )
}

function traderName(label?: string, trader?: string): string {
  if (!trader) return 'Copied from no one'
  return label || shortAddress(trader)
}

// questions names the outcomes the holdings know, for the orders of the same tokens.
function questions(s: Summary): Map<string, string> {
  return new Map(
    s.holdings
      .filter((h) => h.question)
      .map((h) => [h.tokenId, h.outcome ? `${h.question} · ${h.outcome}` : h.question!]),
  )
}

function orderText(o: Order): string {
  if (o.type === 'SETTLE') return `settled at ${o.price}`

  const size = o.dollars ? formatDollars(o.dollars) : `${formatShares(o.shares ?? '0')} shares`
  const limit = o.side === 'buy' ? `up to ${o.price}` : `at least ${o.price}`

  return `${o.side} ${size} at ${limit}, ${o.type}`
}

function resultText(o: Order): string {
  const shares = o.fills.reduce((n, f) => n + Number(f.shares), 0)
  const dollars = o.fills.reduce((n, f) => n + Number(f.dollars), 0)
  const fees = o.fills.reduce((n, f) => n + Number(f.fee), 0)

  if (o.status === 'refused' || o.status === 'unfilled')
    return `${o.status}: ${o.reason ?? ''}`.trim()

  const what = `${o.status}: ${formatShares(String(shares))} shares for ${formatDollars(String(dollars))}`

  return fees > 0 ? `${what}, ${formatDollars(String(fees))} in fees` : what
}
