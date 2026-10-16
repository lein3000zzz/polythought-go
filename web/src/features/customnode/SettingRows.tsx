import { Plus, Trash2 } from 'lucide-react'

import type { Problem } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { ParamField } from '@/features/workflow/ParamField'

import {
  choices,
  fieldKinds,
  newSetting,
  settingKinds,
  specOf,
  type FieldKind,
  type SettingKind,
  type SettingRow,
} from './definition'

type Props = {
  rows: SettingRow[]
  onChange: (rows: SettingRow[]) => void
  problems: readonly Problem[]
}

/** The settings whoever places the node fills in, which the code reads as ctx.params. */
export function SettingRows({ rows, onChange, problems }: Props) {
  const change = (i: number, patch: Partial<SettingRow>) =>
    onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))

  return (
    <div className="flex flex-col gap-3">
      {rows.map((row, i) => (
        <Setting
          key={row.key}
          row={row}
          index={i}
          problems={problems.filter(
            (p) => p.param === `settings.${i}` || p.param?.startsWith(`settings.${i}.`),
          )}
          onChange={(patch) => change(i, patch)}
          onRemove={() => onChange(rows.filter((_, j) => j !== i))}
        />
      ))}

      <Button
        type="button"
        variant="outline"
        size="sm"
        className="self-start"
        onClick={() => onChange([...rows, newSetting()])}
      >
        <Plus />
        Add a setting
      </Button>
    </div>
  )
}

function Setting({
  row,
  index,
  problems,
  onChange,
  onRemove,
}: {
  row: SettingRow
  index: number
  problems: Problem[]
  onChange: (patch: Partial<SettingRow>) => void
  onRemove: () => void
}) {
  const id = `setting-${row.key}`
  const n = index + 1

  return (
    <fieldset className="flex flex-col gap-3 rounded-lg border p-3">
      <legend className="sr-only">Setting {n}</legend>

      <div className="grid grid-cols-[1fr_1fr_auto] items-end gap-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={`${id}-name`}>Name</Label>
          <Input
            id={`${id}-name`}
            value={row.name}
            placeholder="threshold"
            spellCheck={false}
            className="font-mono"
            aria-label={`Setting ${n} name`}
            onChange={(e) => onChange({ name: e.target.value })}
          />
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor={`${id}-kind`}>Kind</Label>
          <NativeSelect
            id={`${id}-kind`}
            value={row.kind}
            aria-label={`Setting ${n} kind`}
            onChange={(e) => onChange({ kind: e.target.value as SettingKind, default: undefined })}
          >
            {settingKinds.map(([kind, label]) => (
              <NativeSelectOption key={kind} value={kind}>
                {label}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>

        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={`Remove setting ${n}`}
          onClick={onRemove}
        >
          <Trash2 />
        </Button>
      </div>

      <div className="grid grid-cols-2 gap-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={`${id}-label`}>Label</Label>
          <Input
            id={`${id}-label`}
            value={row.label}
            placeholder={row.name || 'As its name'}
            aria-label={`Setting ${n} label`}
            onChange={(e) => onChange({ label: e.target.value })}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={`${id}-description`}>Description</Label>
          <Input
            id={`${id}-description`}
            value={row.description}
            aria-label={`Setting ${n} description`}
            onChange={(e) => onChange({ description: e.target.value })}
          />
        </div>
      </div>

      {row.kind === 'choice' && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={`${id}-options`}>Choices, one per line</Label>
          <Textarea
            id={`${id}-options`}
            rows={3}
            value={row.options.join('\n')}
            onChange={(e) => onChange({ options: e.target.value.split('\n'), default: undefined })}
            onBlur={() => onChange({ options: choices(row) })}
          />
        </div>
      )}

      {row.kind === 'field' && (
        <fieldset className="flex flex-col gap-1.5">
          <legend className="text-sm font-medium">The field may be</legend>
          <div className="flex flex-wrap gap-x-4 gap-y-1.5">
            {fieldKinds.map(([kind, label]) => (
              <label key={kind} className="flex items-center gap-1.5 text-sm">
                <Checkbox
                  checked={row.fieldKinds.includes(kind)}
                  onCheckedChange={(on) =>
                    onChange({
                      fieldKinds: on
                        ? [...row.fieldKinds, kind]
                        : row.fieldKinds.filter((k): k is FieldKind => k !== kind),
                    })
                  }
                />
                {label}
              </label>
            ))}
          </div>
          <p className="text-xs text-muted-foreground">Any kind, when none is ticked.</p>
        </fieldset>
      )}

      <div className="flex items-end gap-4">
        {row.kind !== 'wallet' && (
          <div className="flex-1">
            {/* Remade with the kind, so a value typed for another kind does not linger. */}
            <ParamField
              key={`${row.kind}:${row.options.join('|')}`}
              spec={specOf(row)}
              scope={id}
              value={row.default}
              onChange={(value) => onChange({ default: value })}
            />
          </div>
        )}
        <label className="flex items-center gap-1.5 pb-2 text-sm">
          <Checkbox
            checked={row.required}
            onCheckedChange={(on) => onChange({ required: on === true })}
          />
          Required
        </label>
      </div>

      {row.kind === 'wallet' && (
        <p className="text-xs text-muted-foreground">
          One of the wallets of whoever places the node. The code gets its ID, for positions.list.
        </p>
      )}

      {problems.map((p, i) => (
        <p key={i} role="alert" className="text-xs text-destructive">
          {p.message}
        </p>
      ))}
    </fieldset>
  )
}
