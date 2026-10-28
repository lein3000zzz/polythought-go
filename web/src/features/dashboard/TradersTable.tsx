import type { Schemas } from '@/api/client'
import { Table, TableBody, TableCell, TableHeader, TableRow } from '@/components/ui/table'
import { Profit } from '@/features/trading/Profit'
import { formatDollars, shortAddress } from '@/lib/format'

import { type SortValue, sortRows, useSort, winRate } from './sort'
import { SortHeader } from './SortHeader'

type Trader = Schemas['TraderResult']
type Column =
  | 'trader'
  | 'workflows'
  | 'profit'
  | 'realized'
  | 'unrealized'
  | 'positions'
  | 'volume'
  | 'orders'
  | 'winRate'

type Props = { mode: 'paper' | 'live'; traders: Trader[] }

/**
 * What copying each trader came to across the workflows copying them, in the mode shown
 * (D429): who makes you money. The most profitable first to start with.
 */
export function TradersTable({ mode, traders }: Props) {
  const [sort, by] = useSort<Column>({ key: 'profit', dir: 'desc' }, ['trader'])
  const shown = traders.filter((t) => t.mode === mode)

  const value = (t: Trader, key: Column): SortValue => {
    switch (key) {
      case 'trader':
        return t.trader ? (t.label || t.trader).toLowerCase() : undefined
      case 'workflows':
      case 'positions':
      case 'orders':
        return t[key]
      case 'winRate':
        return winRate(t.wins, t.closed)
      default:
        return Number(t[key])
    }
  }

  const head = { sort, onSort: by }

  return (
    <section aria-labelledby="dashboard-traders" className="flex flex-col gap-3">
      <h2 id="dashboard-traders" className="font-medium">
        Traders
      </h2>
      {shown.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No trader copied {mode === 'live' ? 'live' : 'on paper'} yet.
        </p>
      ) : (
        <Table aria-labelledby="dashboard-traders">
          <TableHeader>
            <TableRow>
              <SortHeader label="Trader" column="trader" {...head} />
              <SortHeader label="Workflows" column="workflows" numeric {...head} />
              <SortHeader label="Profit" column="profit" numeric {...head} />
              <SortHeader label="Realized" column="realized" numeric {...head} />
              <SortHeader label="Unrealized" column="unrealized" numeric {...head} />
              <SortHeader label="Positions" column="positions" numeric {...head} />
              <SortHeader label="Volume" column="volume" numeric {...head} />
              <SortHeader label="Orders" column="orders" numeric {...head} />
              <SortHeader label="Win rate" column="winRate" numeric {...head} />
            </TableRow>
          </TableHeader>
          <TableBody>
            {sortRows(shown, sort, value).map((t) => {
              const rate = winRate(t.wins, t.closed)

              return (
                <TableRow key={`${t.mode}-${t.trader ?? ''}`}>
                  <TableCell title={t.trader}>
                    {t.trader ? (
                      <span className="font-medium">{t.label || shortAddress(t.trader)}</span>
                    ) : (
                      <span className="text-muted-foreground">Copied from no one</span>
                    )}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{t.workflows}</TableCell>
                  <TableCell className="text-right">
                    <Profit text={t.profit} />
                  </TableCell>
                  <TableCell className="text-right">
                    <Profit text={t.realized} />
                  </TableCell>
                  <TableCell className="text-right">
                    <Profit text={t.unrealized} />
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{t.positions}</TableCell>
                  <TableCell className="text-right tabular-nums">
                    {formatDollars(t.volume)}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{t.orders}</TableCell>
                  <TableCell className="text-right tabular-nums">
                    {rate === undefined ? (
                      <span className="text-muted-foreground">—</span>
                    ) : (
                      `${rate}%`
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
