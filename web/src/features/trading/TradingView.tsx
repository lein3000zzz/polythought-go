import { cn } from 'cn'
import { RotateCcw } from 'lucide-react'
import { Fragment, useState } from 'react'
import { toast } from 'sonner'

import { errorMessage, type Schemas } from '@/api/client'
import {
  type TradingMode,
  useCancelOrder,
  useOrders,
  useResetPaperAccount,
  useTradingSetup,
  useTradingSummary,
} from '@/api/trading'
import { ConfirmDialog } from '@/components/ConfirmDialog'
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
import { actorSays } from '@/lib/actors'
import { formatDateTime, formatDollars, formatShares, shortAddress } from '@/lib/format'

import { HowItTrades } from './HowItTrades'
import { orderText, resultText, traderName } from './orderText'
import { Profit } from './Profit'

type Summary = Schemas['TradingSummary']
type Order = Schemas['Order']

/**
 * How a workflow trades and how its trading went (D104, D191): paper or live, its paper
 * account or its wallet, each trader it copies, what it holds marked to market, and its
 * orders. The books shown are those of the mode it trades in, or the other's on asking.
 * Refreshed while it shows.
 */
export function TradingView({ workflowId }: { workflowId: string }) {
  const [shown, setShown] = useState<TradingMode>()
  const setup = useTradingSetup(workflowId)
  const summary = useTradingSummary(workflowId, shown)
  const orders = useOrders(workflowId)

  if (summary.isPending) return <p className="p-6 text-sm text-muted-foreground">Loading…</p>
  if (summary.isError)
    return <p className="p-6 text-sm text-destructive">{errorMessage(summary.error)}</p>

  const s = summary.data
  const names = questions(s)
  const mode = s.account.mode
  // Books of both modes once it was ever set up to trade live.
  const both = setup.data?.wallet !== undefined || setup.data?.mode === 'live' || mode === 'live'

  return (
    <div className="flex h-full flex-col gap-8 overflow-y-auto p-6">
      <HowItTrades workflowId={workflowId} />

      {both && (
        <div role="group" aria-label="Books" className="-mb-4 flex gap-2">
          {(['paper', 'live'] as const).map((m) => (
            <Button
              key={m}
              variant={m === mode ? 'secondary' : 'ghost'}
              size="sm"
              aria-pressed={m === mode}
              onClick={() => setShown(m)}
            >
              {m === 'paper' ? 'Paper books' : 'Live books'}
            </Button>
          ))}
        </div>
      )}

      {mode === 'live' ? (
        <LiveAccount account={s.account} />
      ) : (
        <Account workflowId={workflowId} account={s.account} />
      )}

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
          workflowId={workflowId}
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
      {Number(account.held) > 0 && (
        <Figure label="Held by resting orders" value={formatDollars(account.held)} />
      )}
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

/** A workflow's live books (D198): its wallet's pUSD, shared, and what it holds live. */
function LiveAccount({ account }: { account: Summary['account'] }) {
  const wallet = account.wallet
  const name = wallet ? wallet.label || shortAddress(wallet.address) : undefined

  return (
    <section aria-label="Live account" className="flex flex-wrap items-end gap-x-8 gap-y-3">
      <Figure label="Worth" value={account.cashKnown ? formatDollars(account.worth) : '—'} />
      <Figure label="Cash (pUSD)" value={account.cashKnown ? formatDollars(account.cash) : '—'} />
      <Figure label="Profit" value={<Profit text={account.profit} />} />
      <p className="text-xs text-muted-foreground">
        {name
          ? `The pUSD of ${name} on Polymarket, which other workflows trading from it share, and what this workflow holds live.`
          : 'No wallet: choose one to trade live.'}
        {name && !account.cashKnown && ' Its pUSD could not be read just now.'}
      </p>
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

function Orders({
  workflowId,
  orders,
  resetAt,
  names,
}: {
  workflowId: string
  orders: Order[]
  resetAt?: string
  names: Map<string, string>
}) {
  const cancel = useCancelOrder(workflowId)

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
              <TableCell className="whitespace-nowrap">
                {o.mode === 'live' && (
                  <Badge variant="destructive" className="mr-2">
                    live
                  </Badge>
                )}
                {orderText(o)}
                {o.by && (
                  <span className="block text-xs text-muted-foreground">{actorSays(o.by)}</span>
                )}
              </TableCell>
              <TableCell className="max-w-64 truncate" title={names.get(o.tokenId)}>
                {names.get(o.tokenId) ?? shortAddress(o.tokenId)}
              </TableCell>
              <TableCell>
                <span className="flex items-center gap-2">
                  {resultText(o)}
                  {o.status === 'open' && (
                    <Button
                      variant="outline"
                      size="sm"
                      aria-label={`Cancel ${orderText(o)}`}
                      disabled={cancel.isPending}
                      onClick={() =>
                        cancel.mutate(o.id, {
                          onSuccess: () => toast.success('Order cancelled'),
                          onError: (error) => toast.error(errorMessage(error)),
                        })
                      }
                    >
                      Cancel
                    </Button>
                  )}
                </span>
              </TableCell>
            </TableRow>
          </Fragment>
        ))}
      </TableBody>
    </Table>
  )
}

// questions names the outcomes the holdings know, for the orders of the same tokens.
function questions(s: Summary): Map<string, string> {
  return new Map(
    s.holdings
      .filter((h) => h.question)
      .map((h) => [h.tokenId, h.outcome ? `${h.question} · ${h.outcome}` : h.question!]),
  )
}
