import { choices, type FieldRow, type Form, type SettingRow } from './definition'

/**
 * A working starter for the node the form describes (D59): its settings typed for
 * ctx.params, and run returning what its outputs and items take. The form follows it
 * until the code is edited.
 */
export function starter(form: Form): string {
  const settings = form.settings.filter((s) => identifier(s.name.trim()))
  const outputs = form.outputs.map((o) => o.trim()).filter(Boolean)
  const lines = ['import type { Context, Item } from "@polythought/sdk"', '']

  if (settings.length > 0) {
    lines.push('type Settings = {', ...settings.map((s) => `  ${settingLine(s)}`), '}', '')
  }

  const ctx = settings.length > 0 ? 'Context<Settings>' : 'Context'
  const made = form.passed ? 'items' : `items.map(() => (${sample(form.fields, '  ')}))`

  if (outputs.length <= 1) {
    lines.push(
      '// Gets every item at once; returns the items to send on.',
      `export function run(items: Item[], ctx: ${ctx}) {`,
      `  return ${made}`,
      '}',
    )
  } else {
    const [first, ...rest] = outputs
    lines.push(
      '// Gets every item at once; returns the items of each output, by name.',
      `export function run(items: Item[], ctx: ${ctx}) {`,
      '  return {',
      `    ${key(first)}: ${made},`,
      ...rest.map((o) => `    ${key(o)}: [],`),
      '  }',
      '}',
    )
  }

  return lines.join('\n') + '\n'
}

function settingLine(s: SettingRow): string {
  const optional = !s.required && s.default === undefined
  return `${s.name.trim()}${optional ? '?' : ''}: ${settingType(s)}`
}

function settingType(s: SettingRow): string {
  switch (s.kind) {
    case 'number':
      return 'number'
    case 'bool':
      return 'boolean'
    case 'list':
      return 'string[]'
    case 'choice': {
      const options = choices(s)
      return options.length > 0 ? options.map((o) => JSON.stringify(o)).join(' | ') : 'string'
    }
    default:
      return 'string' // text, decimals as exact text, durations, fields and wallet IDs
  }
}

// sample is an item with every required field declared, of a value its kind takes.
function sample(fields: FieldRow[], indent: string): string {
  const required = fields.filter((f) => !f.optional && identifier(f.name.trim()))
  if (required.length === 0) return '{}'

  const inner = indent + '  '
  const lines = required.map((f) => `${inner}${f.name.trim()}: ${value(f, inner)},`)

  return `{\n${lines.join('\n')}\n${indent}}`
}

function value(f: FieldRow, indent: string): string {
  switch (f.kind) {
    case 'text':
      return JSON.stringify(f.options[0] ?? '')
    case 'number':
      return '0'
    case 'decimal':
      return '"0"'
    case 'bool':
      return 'false'
    case 'time':
      return 'ctx.execution.startedAt'
    case 'list':
      return '[]'
    case 'object':
      return sample(f.fields, indent)
    default:
      return 'null'
  }
}

function identifier(name: string): boolean {
  return /^[A-Za-z_][A-Za-z0-9_]*$/.test(name)
}

// key writes an output's name as a key of the returned object, quoted when it needs it.
function key(name: string): string {
  return identifier(name) ? name : JSON.stringify(name)
}
