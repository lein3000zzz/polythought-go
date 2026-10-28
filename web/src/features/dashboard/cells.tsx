import { TableCell } from '@/components/ui/table'
import { Profit } from '@/features/trading/Profit'
import { formatDollars } from '@/lib/format'

/** The dashboards' tables' cells: dollars, a profit in its colours, or a count. */
export function Money({ text, profit }: { text?: string; profit?: boolean }) {
  return (
    <TableCell className="text-right tabular-nums">
      {text === undefined ? <None /> : profit ? <Profit text={text} /> : formatDollars(text)}
    </TableCell>
  )
}

export function Count({ value, suffix = '' }: { value?: number; suffix?: string }) {
  return (
    <TableCell className="text-right tabular-nums">
      {value === undefined ? <None /> : `${value}${suffix}`}
    </TableCell>
  )
}

export function None() {
  return <span className="text-muted-foreground">—</span>
}
