import type { ReactNode } from 'react'

import type { Schemas } from '@/api/client'
import { Profit } from '@/features/trading/Profit'
import { formatDollars } from '@/lib/format'

import { winRate } from './sort'

type Props = {
  mode: 'paper' | 'live'
  totals: Schemas['StatsTotals']
  day: Schemas['RunCounts']
  week: Schemas['RunCounts']
}

/** The workflows trading in one mode, added up, and all their runs lately (D429). */
export function Totals({ mode, totals: t, day, week }: Props) {
  const rate = winRate(t.wins, t.closed)

  return (
    <section aria-labelledby="dashboard-totals" className="flex flex-col gap-3">
      <h2 id="dashboard-totals" className="font-medium">
        Totals
      </h2>
      {t.workflows === 0 && (
        <p className="text-sm text-muted-foreground">
          No workflow has traded {mode === 'live' ? 'live' : 'on paper'} yet.
        </p>
      )}
      <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Figure
          label="Profit"
          note={`${t.workflows} ${t.workflows === 1 ? 'workflow' : 'workflows'} ${mode === 'live' ? 'live' : 'on paper'}`}
        >
          <Profit text={t.profit} />
        </Figure>
        <Figure label="Realized" note="of positions sold, or settled">
          <Profit text={t.realized} />
        </Figure>
        <Figure
          label="Unrealized"
          note={
            t.unpriced > 0
              ? `${t.unpriced} at cost: no price now`
              : 'of positions held, at prices now'
          }
        >
          <Profit text={t.unrealized} />
        </Figure>
        <Figure label="Open positions">{t.positions}</Figure>
        <Figure label="Volume" note={`${t.orders} ${t.orders === 1 ? 'order' : 'orders'} filled`}>
          {formatDollars(t.volume)}
        </Figure>
        <Figure
          label="Win rate"
          note={t.closed > 0 ? `${t.wins} of ${t.closed} closed made money` : 'nothing closed yet'}
        >
          {rate === undefined ? '—' : `${rate}%`}
        </Figure>
        <Figure label="Runs, last day" note={`${day.failed} failed`}>
          {day.runs}
        </Figure>
        <Figure label="Runs, last week" note={`${week.failed} failed`}>
          {week.runs}
        </Figure>
      </dl>
    </section>
  )
}

function Figure({ label, note, children }: { label: string; note?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border p-4">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="text-xl font-medium tabular-nums">{children}</dd>
      {note && <dd className="text-xs text-muted-foreground">{note}</dd>}
    </div>
  )
}
