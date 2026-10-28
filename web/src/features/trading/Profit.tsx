import { cn } from 'cn'

import { formatProfit } from '@/lib/format'

/** +$12.40 in green, −$1.20 in red: a gain or a loss. */
export function Profit({ text, className }: { text: string; className?: string }) {
  const n = Number(text)

  return (
    <span
      className={cn(
        'tabular-nums',
        n > 0 && 'text-emerald-600 dark:text-emerald-400',
        n < 0 && 'text-destructive',
        className,
      )}
    >
      {formatProfit(text)}
    </span>
  )
}
