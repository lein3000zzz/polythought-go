import { useState, type ReactNode } from 'react'

import type { ParamSpec } from '@/api/client'
import { useCredentialList, useCredentialTypes } from '@/api/vault'
import { useWallets } from '@/api/wallets'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { shortAddress } from '@/lib/format'

import { chosen, parseText, textOf } from './params'

type Props = {
  spec: ParamSpec
  value: unknown
  onChange: (value: unknown) => void
  problem?: string // from the server's last look at the workflow
  scope?: string // keeps input IDs apart when several nodes' fields share a form
}

/** The input for one node param, chosen by its kind. */
export function ParamField(props: Props) {
  const { spec } = props

  if (spec.kind === 'bool') return <BoolField {...props} />
  if (spec.kind === 'wallet') return <WalletField {...props} />
  if (spec.kind === 'credential') return <CredentialField {...props} />
  if (spec.options?.length && spec.kind === 'list') return <ChecklistField {...props} />
  if (spec.options?.length) return <SelectField {...props} />
  if (spec.kind === 'list' || spec.kind === 'tokens') return <TextField {...props} multiline />

  return <TextField {...props} />
}

const placeholders: Partial<Record<ParamSpec['kind'], string>> = {
  duration: 'e.g. 30s, 5m, 1h',
  tokens: 'One token ID per line',
  list: 'One per line',
}

function TextField({
  spec,
  value,
  onChange,
  problem,
  scope,
  multiline,
}: Props & { multiline?: boolean }) {
  // The text is kept as typed: a value that does not parse yet still shows.
  const [text, setText] = useState(() => textOf(value))
  const parsed = parseText(spec, text)
  const id = fieldId(spec, scope)

  const inputProps = {
    id,
    value: text,
    placeholder:
      spec.default !== undefined ? `Default: ${textOf(spec.default)}` : placeholders[spec.kind],
    'aria-invalid': Boolean(parsed.problem ?? problem) || undefined,
    'aria-describedby': `${id}-hint`,
    onChange: (e: { target: { value: string } }) => {
      setText(e.target.value)
      onChange(parseText(spec, e.target.value).value)
    },
  }

  return (
    <Field spec={spec} scope={scope} problem={parsed.problem ?? problem}>
      {multiline ? (
        <Textarea rows={3} className="font-mono text-xs" {...inputProps} />
      ) : (
        <Input
          inputMode={
            spec.kind === 'number' ? 'numeric' : spec.kind === 'decimal' ? 'decimal' : undefined
          }
          {...inputProps}
        />
      )}
    </Field>
  )
}

function SelectField({ spec, value, onChange, problem, scope }: Props) {
  const current = typeof value === 'string' ? value : ''

  return (
    <Field spec={spec} scope={scope} problem={problem}>
      <NativeSelect
        id={fieldId(spec, scope)}
        className="w-full"
        value={current}
        aria-invalid={Boolean(problem) || undefined}
        onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value)}
      >
        <NativeSelectOption value="">
          {spec.default !== undefined ? `Default (${textOf(spec.default)})` : 'Choose…'}
        </NativeSelectOption>
        {spec.options?.map((o) => (
          <NativeSelectOption key={o} value={o}>
            {o}
          </NativeSelectOption>
        ))}
        {current && !spec.options?.includes(current) && (
          <NativeSelectOption value={current}>{current} (not an option)</NativeSelectOption>
        )}
      </NativeSelect>
    </Field>
  )
}

function ChecklistField({ spec, value, onChange, problem, scope }: Props) {
  const selected = chosen(spec, value)

  const toggle = (option: string, on: boolean) => {
    const next = on ? [...selected, option] : selected.filter((o) => o !== option)
    // Keep the offered order; choosing nothing falls back to the default.
    const ordered = (spec.options ?? []).filter((o) => next.includes(o))
    onChange(ordered.length > 0 ? ordered : undefined)
  }

  return (
    <Field spec={spec} scope={scope} problem={problem} group>
      <div className="flex flex-wrap gap-x-4 gap-y-2">
        {spec.options?.map((o) => {
          const id = `${fieldId(spec, scope)}-${o}`
          return (
            <div key={o} className="flex items-center gap-1.5">
              <Checkbox
                id={id}
                checked={selected.includes(o)}
                onCheckedChange={(on) => toggle(o, on === true)}
              />
              <Label htmlFor={id} className="font-normal">
                {o}
              </Label>
            </div>
          )
        })}
      </div>
    </Field>
  )
}

function BoolField({ spec, value, onChange, problem, scope }: Props) {
  const checked = typeof value === 'boolean' ? value : spec.default === true

  return (
    <Field spec={spec} scope={scope} problem={problem} inline>
      <Switch id={fieldId(spec, scope)} checked={checked} onCheckedChange={onChange} />
    </Field>
  )
}

function WalletField({ spec, value, onChange, problem, scope }: Props) {
  const wallets = useWallets()
  const current = typeof value === 'string' ? value : ''
  const known = wallets.data?.some((w) => w.id === current)

  return (
    <Field
      spec={spec}
      scope={scope}
      problem={problem}
      hint={wallets.data?.length === 0 ? 'No wallets linked yet.' : undefined}
    >
      <NativeSelect
        id={fieldId(spec, scope)}
        className="w-full"
        value={current}
        disabled={wallets.isPending}
        aria-invalid={Boolean(problem) || undefined}
        onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value)}
      >
        <NativeSelectOption value="">
          {wallets.isPending ? 'Loading wallets…' : 'Choose a wallet'}
        </NativeSelectOption>
        {wallets.data?.map((w) => (
          <NativeSelectOption key={w.id} value={w.id}>
            {w.label || 'Wallet'} · {shortAddress(w.address)}
          </NativeSelectOption>
        ))}
        {current && wallets.isSuccess && !known && (
          <NativeSelectOption value={current}>Unknown wallet ({current})</NativeSelectOption>
        )}
      </NativeSelect>
    </Field>
  )
}

/** One of the user's credentials of the kind the param asks for. */
function CredentialField({ spec, value, onChange, problem, scope }: Props) {
  const credentials = useCredentialList()
  const types = useCredentialTypes()
  const current = typeof value === 'string' ? value : ''
  const matching = credentials.data?.filter((c) => c.type === spec.credentialType) ?? []
  const known = matching.some((c) => c.id === current)
  const kind = types.data?.find((t) => t.name === spec.credentialType)?.label ?? 'credential'

  return (
    <Field
      spec={spec}
      scope={scope}
      problem={problem}
      hint={
        credentials.isSuccess && matching.length === 0
          ? `You have no ${kind} credentials yet: add one on the Credentials page.`
          : undefined
      }
    >
      <NativeSelect
        id={fieldId(spec, scope)}
        className="w-full"
        value={current}
        disabled={credentials.isPending}
        aria-invalid={Boolean(problem) || undefined}
        onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value)}
      >
        <NativeSelectOption value="">
          {credentials.isPending
            ? 'Loading credentials…'
            : spec.required
              ? 'Choose a credential'
              : 'None'}
        </NativeSelectOption>
        {matching.map((c) => (
          <NativeSelectOption key={c.id} value={c.id}>
            {c.name}
          </NativeSelectOption>
        ))}
        {current && credentials.isSuccess && !known && (
          <NativeSelectOption value={current}>Missing credential ({current})</NativeSelectOption>
        )}
      </NativeSelect>
    </Field>
  )
}

function fieldId(spec: ParamSpec, scope?: string) {
  return scope ? `${scope}-param-${spec.name}` : `param-${spec.name}`
}

type FieldProps = {
  spec: ParamSpec
  scope?: string
  problem?: string
  hint?: string
  inline?: boolean // label and control on one line, for switches
  group?: boolean // several controls, labelled as a group
  children: ReactNode
}

function Field({ spec, scope, problem, hint, inline, group, children }: FieldProps) {
  const id = fieldId(spec, scope)
  const label = (
    <span className="text-sm font-medium">
      {spec.label || spec.name}
      {spec.required && <span className="text-muted-foreground"> (required)</span>}
    </span>
  )
  const notes = (
    <div id={`${id}-hint`} className="flex flex-col gap-1 text-xs">
      {spec.sensitive && <p className="text-muted-foreground">Private: sharing leaves it out.</p>}
      {(spec.description || hint) && (
        <p className="text-muted-foreground">
          {[spec.description, hint].filter(Boolean).join(' ')}
        </p>
      )}
      {problem && (
        <p role="alert" className="text-destructive">
          {problem}
        </p>
      )}
    </div>
  )

  if (group) {
    return (
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2">{label}</legend>
        {children}
        {notes}
      </fieldset>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      <div className={inline ? 'flex items-center justify-between gap-2' : 'contents'}>
        <Label htmlFor={id}>{label}</Label>
        {children}
      </div>
      {notes}
    </div>
  )
}
