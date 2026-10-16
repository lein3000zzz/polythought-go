import { Plus, Trash2 } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { fieldKinds, newField, type FieldKind, type FieldRow } from './definition'

type Props = {
  rows: FieldRow[]
  onChange: (rows: FieldRow[]) => void
  path?: string // where these fields are, for labels: "best." or "outcomes[]."
}

/**
 * The fields of the node's own items, one row each (D58). An object opens rows for its
 * fields, and a list a row for what each element is.
 */
export function FieldRows({ rows, onChange, path = '' }: Props) {
  const change = (i: number, next: FieldRow) => onChange(rows.map((r, j) => (j === i ? next : r)))

  return (
    <div className="flex flex-col gap-2">
      {rows.map((row, i) => (
        <Field
          key={row.key}
          row={row}
          path={path}
          onChange={(next) => change(i, next)}
          onRemove={() => onChange(rows.filter((_, j) => j !== i))}
        />
      ))}

      <Button
        type="button"
        variant="outline"
        size="sm"
        className="self-start"
        onClick={() => onChange([...rows, newField()])}
      >
        <Plus />
        {path ? `Add a field to ${path.replace(/\.$/, '')}` : 'Add a field'}
      </Button>
    </div>
  )
}

function Field({
  row,
  path,
  element,
  onChange,
  onRemove,
}: {
  row: FieldRow
  path: string
  element?: boolean // a list's elements: no name, never missing
  onChange: (next: FieldRow) => void
  onRemove?: () => void
}) {
  const named = element ? path.replace(/\.$/, '') : path + (row.name || 'field')
  const patch = (p: Partial<FieldRow>) => onChange({ ...row, ...p })

  const setKind = (kind: FieldKind) =>
    onChange({
      ...newField(kind, row.name),
      key: row.key,
      description: row.description,
      optional: row.optional,
      nullable: row.nullable,
    })

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {element ? (
          <span className="text-sm text-muted-foreground">Each element is</span>
        ) : (
          <Input
            value={row.name}
            placeholder="name"
            spellCheck={false}
            className="w-36 font-mono"
            aria-label={`Field ${path}name`}
            onChange={(e) => patch({ name: e.target.value })}
          />
        )}

        <NativeSelect
          value={row.kind}
          aria-label={`Kind of ${named}`}
          onChange={(e) => setKind(e.target.value as FieldKind)}
        >
          {fieldKinds.map(([kind, label]) => (
            <NativeSelectOption key={kind} value={kind}>
              {label}
            </NativeSelectOption>
          ))}
        </NativeSelect>

        {!element && (
          <label className="flex items-center gap-1.5 text-sm">
            <Checkbox
              checked={row.optional}
              onCheckedChange={(on) => patch({ optional: on === true })}
            />
            May be missing
          </label>
        )}
        <label className="flex items-center gap-1.5 text-sm">
          <Checkbox
            checked={row.nullable}
            onCheckedChange={(on) => patch({ nullable: on === true })}
          />
          May be null
        </label>

        {onRemove && (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={`Remove ${named}`}
            onClick={onRemove}
          >
            <Trash2 />
          </Button>
        )}
      </div>

      <div className="flex flex-wrap gap-2">
        <Input
          value={row.description}
          placeholder="What it holds"
          className="min-w-48 flex-1"
          aria-label={`Description of ${named}`}
          onChange={(e) => patch({ description: e.target.value })}
        />
        {row.kind === 'text' && (
          <Input
            value={row.options.join(',')}
            placeholder="Only these values, separated by commas"
            className="min-w-48 flex-1"
            aria-label={`Values of ${named}`}
            onChange={(e) => patch({ options: e.target.value ? e.target.value.split(',') : [] })}
          />
        )}
      </div>

      {row.kind === 'object' && (
        <div className="border-l pl-4">
          <FieldRows
            rows={row.fields}
            path={`${named}.`}
            onChange={(fields) => patch({ fields })}
          />
        </div>
      )}

      {row.kind === 'list' && row.of && (
        <div className="border-l pl-4">
          <Field row={row.of} path={`${named}[].`} element onChange={(of) => patch({ of })} />
        </div>
      )}
    </div>
  )
}
