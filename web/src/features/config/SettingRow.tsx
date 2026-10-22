import { Lock, RotateCcw } from 'lucide-react'

import type { ParamSpec } from '@/api/client'
import type { Setting } from '@/api/settings'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ParamField } from '@/features/workflow/ParamField'

import { locked } from './pages'

const kinds: Record<Setting['kind'], ParamSpec['kind']> = {
  string: 'string',
  int: 'number',
  bool: 'bool',
  duration: 'duration',
  enum: 'string',
  list: 'list',
}

/** The setting as a param, so it gets the same field a node's setting would. */
function paramSpec(s: Setting): ParamSpec {
  const empty = Array.isArray(s.default) && s.default.length === 0

  return {
    name: s.key,
    label: s.label,
    kind: kinds[s.kind],
    options: s.options,
    default: empty ? undefined : s.default,
    description: s.description,
    required: false,
  }
}

type RowProps = {
  setting: Setting
  draft: unknown
  hasDraft: boolean
  problem?: string
  onChange: (value: unknown) => void // undefined drops the draft
  onReset: () => void
}

export function SettingRow({ setting: s, draft, hasDraft, problem, onChange, onReset }: RowProps) {
  const isLocked = locked(s)
  // A setting at its default shows the default as a placeholder, so typing sets it.
  const value = hasDraft ? (draft ?? undefined) : s.source === 'default' ? undefined : s.value
  const resettable = !isLocked && draft !== null && (s.secret ? s.isSet : s.source === 'database')

  return (
    <div className="flex flex-col gap-2">
      <fieldset disabled={isLocked} className="min-w-0">
        {s.secret ? (
          <SecretField setting={s} draft={draft} problem={problem} onChange={onChange} />
        ) : (
          <ParamField
            spec={paramSpec(s)}
            scope="setting"
            value={value}
            problem={problem}
            // A cleared field means the default.
            onChange={(v) => onChange(v === undefined ? null : v)}
          />
        )}
      </fieldset>

      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        {isLocked ? (
          <Badge variant="secondary">
            <Lock />
            {s.source === 'file' ? 'Set in the config file' : `Set by ${s.env}`}
          </Badge>
        ) : (
          <Badge variant="outline">{s.source === 'database' ? 'Saved' : 'Default'}</Badge>
        )}
        {isLocked && (
          <span>
            {s.source === 'file'
              ? `Change ${s.key} in the server's config file.`
              : "Change it in the server's environment."}
          </span>
        )}
        {s.apply === 'restart' && !s.pending && <span>Applies after a restart.</span>}
        {s.pending && <Badge variant="outline">Waiting for a restart</Badge>}
        {hasDraft && draft === null && !isLocked && <span>Resets to the default when saved.</span>}
        {resettable && (
          <Button variant="ghost" size="xs" onClick={onReset}>
            <RotateCcw />
            {s.secret ? 'Clear' : 'Reset to default'}
          </Button>
        )}
      </div>
    </div>
  )
}

type SecretProps = {
  setting: Setting
  draft: unknown
  problem?: string
  onChange: (value: unknown) => void
}

/** A secret is never shown: it can only be replaced or cleared. */
function SecretField({ setting: s, draft, problem, onChange }: SecretProps) {
  const id = `setting-param-${s.key}`

  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{s.label}</Label>
      <Input
        id={id}
        type="password"
        autoComplete="off"
        value={typeof draft === 'string' ? draft : ''}
        placeholder={s.isSet ? 'Saved. Type a new value to replace it.' : 'Not set'}
        aria-invalid={Boolean(problem) || undefined}
        aria-describedby={`${id}-hint`}
        onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value)}
      />
      <div id={`${id}-hint`} className="flex flex-col gap-1 text-xs">
        <p className="text-muted-foreground">
          {s.description} Stored encrypted and never shown again.
        </p>
        {problem && (
          <p role="alert" className="text-destructive">
            {problem}
          </p>
        )}
      </div>
    </div>
  )
}
