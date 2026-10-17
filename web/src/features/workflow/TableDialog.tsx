import { cn } from 'cn'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'

import type { ParamSpec } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { parseText, textOf } from './params'
import { rowProblems, rowsOf, type Row } from './table'

type Props = {
  spec: ParamSpec
  value: unknown
  onChange: (rows: Row[] | undefined) => void
  onClose: () => void
  problem?: string // from the server's last look at the workflow
}

/**
 * A table's rows, to add, edit, reorder and remove (D81). Changes apply as they are made,
 * as in the rest of the settings. Cells are plain values: only the whole table takes an
 * expression (D82).
 */
export function TableDialog({ spec, value, onChange, onClose, problem }: Props) {
  const columns = spec.columns ?? []
  const label = spec.label || spec.name

  // Each row keeps a key while the dialog is open, so a row moved keeps what is typed in it.
  const [rows, setRows] = useState(() => rowsOf(value).map((cells, key) => ({ key, cells })))
  const [nextKey, setNextKey] = useState(rows.length)
  const problems = rowProblems(
    columns,
    rows.map((r) => r.cells),
  )

  const update = (next: typeof rows) => {
    setRows(next)
    onChange(next.length > 0 ? next.map((r) => r.cells) : undefined)
  }

  const setCell = (key: number, column: string, cell: unknown) =>
    update(
      rows.map((r) => {
        if (r.key !== key) return r

        const cells = { ...r.cells, [column]: cell }
        if (cell === undefined) delete cells[column] // empty: the column's default
        return { ...r, cells }
      }),
    )

  const move = (i: number, by: number) => {
    const next = [...rows]
    ;[next[i], next[i + by]] = [next[i + by], next[i]]
    update(next)
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle>{label}</DialogTitle>
          <DialogDescription>
            {spec.description || 'A row each.'} Changes apply as you make them.
          </DialogDescription>
        </DialogHeader>

        <div className="max-h-[60vh] overflow-auto">
          <table className="w-full border-separate border-spacing-x-1 border-spacing-y-1 text-sm">
            <thead>
              <tr className="text-left text-xs text-muted-foreground">
                {columns.map((c) => (
                  <th key={c.name} scope="col" className="font-medium" title={c.description}>
                    {c.label || c.name}
                    {c.required && c.default === undefined && ' *'}
                  </th>
                ))}
                <th>
                  <span className="sr-only">Row actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={r.key}>
                  {columns.map((c) => (
                    <td key={c.name}>
                      <Cell
                        column={c}
                        row={i}
                        value={r.cells[c.name]}
                        onChange={(cell) => setCell(r.key, c.name, cell)}
                      />
                    </td>
                  ))}
                  <td className="whitespace-nowrap">
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Move row ${i + 1} up`}
                      disabled={i === 0}
                      onClick={() => move(i, -1)}
                    >
                      <ArrowUp />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Move row ${i + 1} down`}
                      disabled={i === rows.length - 1}
                      onClick={() => move(i, 1)}
                    >
                      <ArrowDown />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Remove row ${i + 1}`}
                      onClick={() => update(rows.filter((x) => x.key !== r.key))}
                    >
                      <Trash2 />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          {rows.length === 0 && <p className="py-2 text-muted-foreground">No rows yet.</p>}
        </div>

        {(problems.length > 0 || problem) && (
          <ul role="alert" className="flex flex-col gap-0.5 text-xs text-destructive">
            {problem && <li>{problem}</li>}
            {problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        )}

        <DialogFooter className="sm:justify-between">
          <Button
            type="button"
            variant="outline"
            onClick={() => {
              update([...rows, { key: nextKey, cells: {} }])
              setNextKey(nextKey + 1)
            }}
          >
            <Plus />
            Add a row
          </Button>
          <Button type="button" onClick={onClose}>
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type CellProps = {
  column: ParamSpec
  row: number
  value: unknown
  onChange: (value: unknown) => void
}

// Cell is the input for one cell, by its column's kind.
function Cell({ column, row, value, onChange }: CellProps) {
  const name = `${column.label || column.name}, row ${row + 1}`

  if (column.kind === 'bool') {
    return (
      <Checkbox
        aria-label={name}
        checked={typeof value === 'boolean' ? value : column.default === true}
        onCheckedChange={(on) => onChange(on === true)}
      />
    )
  }

  if (column.options?.length) {
    const current = typeof value === 'string' ? value : ''

    return (
      <NativeSelect
        aria-label={name}
        size="sm"
        className="w-full"
        value={current}
        onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value)}
      >
        <NativeSelectOption value="">
          {column.default !== undefined ? `Default (${textOf(column.default)})` : '—'}
        </NativeSelectOption>
        {column.options.map((o) => (
          <NativeSelectOption key={o} value={o}>
            {o}
          </NativeSelectOption>
        ))}
      </NativeSelect>
    )
  }

  return <TextCell column={column} name={name} value={value} onChange={onChange} />
}

// TextCell keeps its text as typed, as text settings do: a value that does not parse
// yet still shows.
function TextCell({ column, name, value, onChange }: Omit<CellProps, 'row'> & { name: string }) {
  const [text, setText] = useState(() => textOf(value))
  const parsed = parseText(column, text)

  return (
    <Input
      aria-label={name}
      value={text}
      className={cn('h-8', column.kind === 'address' && 'font-mono text-xs')}
      inputMode={
        column.kind === 'number' ? 'numeric' : column.kind === 'decimal' ? 'decimal' : undefined
      }
      placeholder={
        column.default !== undefined
          ? textOf(column.default)
          : column.kind === 'address'
            ? '0x…'
            : undefined
      }
      aria-invalid={Boolean(parsed.problem) || undefined}
      title={parsed.problem}
      onChange={(e) => {
        setText(e.target.value)
        onChange(parseText(column, e.target.value).value)
      }}
    />
  )
}
