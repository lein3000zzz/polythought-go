import type { ParamSpec } from '@/api/client'

import { parseText } from './params'

export type Row = Record<string, unknown>

/** The rows of a table's value: none for anything else, like an expression. */
export function rowsOf(value: unknown): Row[] {
  if (!Array.isArray(value)) return []

  return value.filter((r): r is Row => typeof r === 'object' && r !== null && !Array.isArray(r))
}

/** What is wrong in the rows that the editor can tell: a cell missing, or not its kind. */
export function rowProblems(columns: readonly ParamSpec[], rows: readonly Row[]): string[] {
  const out: string[] = []

  rows.forEach((row, i) => {
    for (const c of columns) {
      const cell = row[c.name]
      const name = c.label || c.name

      if (cell === undefined || cell === null || cell === '') {
        if (c.required && c.default === undefined) out.push(`Row ${i + 1}: ${name} is required`)
      } else if (typeof cell === 'string') {
        const problem = parseText(c, cell).problem
        if (problem) out.push(`Row ${i + 1}, ${name}: ${problem}`)
      }
    }
  })

  return out
}
