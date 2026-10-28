import { useState } from 'react'

export type Sort<K extends string> = { key: K; dir: 'asc' | 'desc' }

/** A value to sort by: a number or text; undefined for none, which sorts last either way. */
export type SortValue = number | string | undefined

/** The rows sorted by value at sort's key: none last, whichever way. */
export function sortRows<T, K extends string>(
  rows: T[],
  sort: Sort<K>,
  value: (row: T, key: K) => SortValue,
): T[] {
  return [...rows].sort((a, b) => {
    const x = value(a, sort.key)
    const y = value(b, sort.key)

    if (x === undefined || y === undefined) return x === y ? 0 : x === undefined ? 1 : -1

    const c =
      typeof x === 'string' && typeof y === 'string' ? x.localeCompare(y) : Number(x) - Number(y)

    return sort.dir === 'asc' ? c : -c
  })
}

/** A table's sort: a column clicked again turns it the other way; text first a to z. */
export function useSort<K extends string>(initial: Sort<K>, texts: K[]) {
  const [sort, setSort] = useState(initial)

  const by = (key: K) =>
    setSort((s) =>
      s.key === key
        ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' }
        : { key, dir: texts.includes(key) ? 'asc' : 'desc' },
    )

  return [sort, by] as const
}

/** Of the closed positions, those that made money: 67%; none when nothing closed. */
export function winRate(wins: number, closed: number): number | undefined {
  return closed > 0 ? Math.round((wins / closed) * 100) : undefined
}
