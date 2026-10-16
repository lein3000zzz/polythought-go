import type { CustomNode, CustomNodeDefinition, CustomSetting, ItemField } from '@/api/customNodes'
import type { ParamSpec, Problem } from '@/api/client'

/**
 * The kinds the form offers for a setting (D54). Choice and field are text the server
 * takes with options, or as a path to a field of the items.
 */
export type SettingKind =
  'string' | 'number' | 'decimal' | 'bool' | 'duration' | 'list' | 'choice' | 'field' | 'wallet'

export const settingKinds: [SettingKind, string][] = [
  ['string', 'Text'],
  ['number', 'Whole number'],
  ['decimal', 'Decimal'],
  ['bool', 'True or false'],
  ['duration', 'Duration'],
  ['list', 'List of text'],
  ['choice', 'One of a few choices'],
  ['field', 'A field of the items'],
  ['wallet', 'Wallet'],
]

export type FieldKind = ItemField['kind']

export const fieldKinds: [FieldKind, string][] = [
  ['text', 'Text'],
  ['number', 'Number'],
  ['decimal', 'Decimal'],
  ['bool', 'True or false'],
  ['time', 'Time'],
  ['list', 'List'],
  ['object', 'Object'],
  ['any', 'Anything'],
]

export type SettingRow = {
  key: string // keeps React's rows apart; not sent
  name: string
  label: string
  description: string
  kind: SettingKind
  required: boolean
  default?: unknown
  options: string[] // of a choice
  fieldKinds: FieldKind[] // of a field; empty means any
}

export type FieldRow = {
  key: string
  name: string // empty for a list's elements
  kind: FieldKind
  description: string
  optional: boolean
  nullable: boolean
  options: string[] // the values text may take
  of?: FieldRow // a list's elements
  fields: FieldRow[] // an object's
}

/** A custom node's form as it is being edited. */
export type Form = {
  name: string
  label: string
  description: string
  settings: SettingRow[]
  outputs: string[]
  passed: boolean // passes the items on, or makes its own with fields
  fields: FieldRow[]
  source: string
}

let next = 0

/** A key for a new row. */
export function rowKey(): string {
  next += 1
  return `row-${next}`
}

export function emptyForm(): Form {
  return {
    name: '',
    label: '',
    description: '',
    settings: [],
    outputs: ['main'],
    passed: true,
    fields: [],
    source: '',
  }
}

export function newSetting(): SettingRow {
  return {
    key: rowKey(),
    name: '',
    label: '',
    description: '',
    kind: 'string',
    required: false,
    options: [],
    fieldKinds: [],
  }
}

export function newField(kind: FieldKind = 'text', name = ''): FieldRow {
  return {
    key: rowKey(),
    name,
    kind,
    description: '',
    optional: false,
    nullable: false,
    options: [],
    of: kind === 'list' ? newField('text') : undefined,
    fields: kind === 'object' ? [newField('text', '')] : [],
  }
}

/** The form for a custom node the server has. */
export function formOf(node: CustomNode): Form {
  return {
    name: node.name,
    label: node.label,
    description: node.description ?? '',
    settings: node.settings.map(settingRow),
    outputs: [...node.outputs],
    passed: node.items.passed,
    fields: node.items.fields.map(fieldRow),
    source: node.source,
  }
}

function settingRow(s: CustomSetting): SettingRow {
  const kind: SettingKind = s.field
    ? 'field'
    : s.kind === 'string' && s.options?.length
      ? 'choice'
      : s.kind

  return {
    key: rowKey(),
    name: s.name,
    label: s.label === s.name ? '' : (s.label ?? ''), // the server labels a setting by its name
    description: s.description ?? '',
    kind,
    required: s.required ?? false,
    default: s.default,
    options: s.options ?? [],
    fieldKinds: (s.fieldKinds ?? []) as FieldKind[],
  }
}

function fieldRow(f: ItemField): FieldRow {
  return {
    key: rowKey(),
    name: f.name,
    kind: f.kind,
    description: f.description ?? '',
    optional: f.optional,
    nullable: f.nullable,
    options: f.options ?? [],
    of: f.of ? fieldRow(f.of) : undefined,
    fields: (f.fields ?? []).map(fieldRow),
  }
}

/** What the form sends the server. */
export function definitionOf(form: Form): CustomNodeDefinition {
  return {
    name: form.name.trim(),
    label: form.label,
    description: form.description,
    settings: form.settings.map(settingOf),
    outputs: form.outputs.map((o) => o.trim()),
    items: { passed: form.passed, fields: form.passed ? [] : form.fields.map(fieldOf) },
    source: form.source,
  }
}

function settingOf(row: SettingRow): CustomSetting {
  const s: CustomSetting = {
    name: row.name.trim(),
    kind: row.kind === 'choice' || row.kind === 'field' ? 'string' : row.kind,
  }

  if (row.label.trim()) s.label = row.label.trim()
  if (row.description.trim()) s.description = row.description.trim()
  if (row.required) s.required = true
  if (row.default !== undefined && row.kind !== 'wallet') s.default = row.default
  if (row.kind === 'choice') s.options = choices(row)
  if (row.kind === 'field') {
    s.field = true
    if (row.fieldKinds.length > 0) s.fieldKinds = row.fieldKinds
  }

  return s
}

function fieldOf(row: FieldRow): ItemField {
  const f: ItemField = {
    name: row.name.trim(),
    kind: row.kind,
    optional: row.optional,
    nullable: row.nullable,
  }

  if (row.description.trim()) f.description = row.description.trim()
  const options = row.options.map((o) => o.trim()).filter(Boolean)
  if (row.kind === 'text' && options.length > 0) f.options = options
  if (row.kind === 'list' && row.of) f.of = fieldOf(row.of)
  if (row.kind === 'object') f.fields = row.fields.map(fieldOf)

  return f
}

/** A choice setting's choices, as typed one per line. */
export function choices(row: SettingRow): string[] {
  return row.options.map((o) => o.trim()).filter(Boolean)
}

/** A setting as a node type has it, for its default's input. */
export function specOf(row: SettingRow): ParamSpec {
  return {
    name: row.name || 'setting',
    label: 'Default',
    kind: row.kind === 'choice' || row.kind === 'field' ? 'string' : row.kind,
    required: false,
    options: row.kind === 'choice' && choices(row).length > 0 ? choices(row) : undefined,
  }
}

/** The problems the server found with one part of the form: name, settings.0.default. */
export function problemsAt(problems: readonly Problem[], param: string): string[] {
  return problems.filter((p) => p.param === param).map((p) => p.message)
}

/** Problems about no part of the form, or one it does not show on its own. */
export function otherProblems(
  problems: readonly Problem[],
  shown: (param: string) => boolean,
): Problem[] {
  return problems.filter((p) => !p.param || !shown(p.param))
}
