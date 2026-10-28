import { ArrowDown, ArrowUp } from 'lucide-react'

import { TableHead } from '@/components/ui/table'

import type { Sort } from './sort'

type HeaderProps<K extends string> = {
  label: string
  column: K
  sort: Sort<K>
  onSort: (key: K) => void
  numeric?: boolean
}

/** A column's head, sorting by it when clicked, and saying how it sorts. */
export function SortHeader<K extends string>({
  label,
  column,
  sort,
  onSort,
  numeric,
}: HeaderProps<K>) {
  const active = sort.key === column

  return (
    <TableHead
      aria-sort={active ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}
      className={numeric ? 'text-right' : undefined}
    >
      <button
        type="button"
        className="inline-flex items-center gap-1 hover:text-foreground"
        onClick={() => onSort(column)}
      >
        {label}
        {active &&
          (sort.dir === 'asc' ? <ArrowUp className="size-3" /> : <ArrowDown className="size-3" />)}
      </button>
    </TableHead>
  )
}
