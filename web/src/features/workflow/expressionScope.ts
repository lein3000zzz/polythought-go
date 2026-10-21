import type { ParamSpec } from '@/api/client'

import type { ItemField, Shape } from './shapes'

// Names an expression sees always mean these, whatever the item's fields (D38).
const always = new Set(['item', 'params', 'execution'])

// Words a field cannot be declared as.
const reserved = new Set(
  (
    'break case catch class const continue debugger default delete do else enum export ' +
    'extends false finally for function if import in instanceof new null return super ' +
    'switch this throw true try typeof var void while with yield let static implements ' +
    'interface package private protected public await arguments eval undefined'
  ).split(' '),
)

/**
 * What an expression in one of a node's settings sees, as TypeScript declarations: the
 * fields of the items there by name, item, params (the node's settings), execution and
 * dec, as the server works expressions out, and items for a node that sees them all
 * (D257). Fields nobody knows make item an Item.
 */
export function scopeDeclarations(
  item: Shape,
  settings: readonly ParamSpec[],
  seesItems = false,
): string {
  const fields = (item ?? []).filter(
    (f) =>
      identifier(f.name) &&
      !always.has(f.name) &&
      !(seesItems && f.name === 'items') &&
      !reserved.has(f.name),
  )
  const itemType = item ? objectType(item) : 'Item'
  const shadowsDec = fields.some((f) => f.name === 'dec')

  const lines = [
    shadowsDec ? '' : 'import { dec } from "@polythought/sdk"',
    'import type { DecimalText, Execution, Item, Json, TimeText } from "@polythought/sdk"',
    '',
    `declare const item: ${itemType}`,
    ...(seesItems ? [`declare const items: (${itemType})[]`] : []),
    `declare const params: ${paramsType(settings)}`,
    'declare const execution: Execution',
    ...fields.map(
      (f) => `declare const ${f.name}: ${fieldType(f)}${f.optional ? ' | undefined' : ''}`,
    ),
    'export {}',
    '',
  ]

  return lines.join('\n')
}

function fieldType(f: ItemField): string {
  const base = kindType(f)
  return f.nullable ? `${base} | null` : base
}

function kindType(f: ItemField): string {
  switch (f.kind) {
    case 'text':
      return f.options?.length ? f.options.map((o) => JSON.stringify(o)).join(' | ') : 'string'
    case 'number':
      return 'number'
    case 'decimal':
      return 'DecimalText'
    case 'bool':
      return 'boolean'
    case 'time':
      return 'TimeText'
    case 'list':
      return f.of ? `(${fieldType(f.of)})[]` : 'Json[]'
    case 'object':
      return objectType(f.fields ?? [])
    default:
      return 'Json'
  }
}

function objectType(fields: readonly ItemField[]): string {
  if (fields.length === 0) return '{}'

  const members = fields.map(
    (f) =>
      `${identifier(f.name) ? f.name : JSON.stringify(f.name)}${f.optional ? '?' : ''}: ${fieldType(f)}`,
  )

  return `{ ${members.join('; ')} }`
}

function paramsType(settings: readonly ParamSpec[]): string {
  if (settings.length === 0) return '{}'

  const members = settings.map(
    (s) => `${identifier(s.name) ? s.name : JSON.stringify(s.name)}?: ${paramType(s)}`,
  )

  return `{ ${members.join('; ')} }`
}

function paramType(s: ParamSpec): string {
  switch (s.kind) {
    case 'number':
      return 'number'
    case 'bool':
      return 'boolean'
    case 'decimal':
      return 'DecimalText'
    case 'list':
    case 'tokens':
      return 'string[]'
    case 'table':
      return `{ ${(s.columns ?? [])
        .map((c) => `${identifier(c.name) ? c.name : JSON.stringify(c.name)}?: ${paramType(c)}`)
        .join('; ')} }[]`
    default:
      return s.options?.length ? s.options.map((o) => JSON.stringify(o)).join(' | ') : 'string'
  }
}

function identifier(name: string): boolean {
  return /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name)
}
