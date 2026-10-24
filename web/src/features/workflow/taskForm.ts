/**
 * The form a run's task is answered with in the app (D413), by its answer's shape: text, a
 * field each where the schema is one the Fields table makes, else JSON.
 */

export type TaskField = {
  name: string
  kind: 'text' | 'number' | 'integer' | 'boolean' | 'list'
  options?: string[] // the answers allowed, for text
  optional: boolean
  description?: string
}

/** A field's value as typed: text for most, checked or not for true or false. */
export type FieldValue = string | boolean

type JsonSchema = {
  type?: string
  enum?: unknown[]
  items?: JsonSchema
  anyOf?: JsonSchema[]
  description?: string
  properties?: Record<string, JsonSchema>
  required?: string[]
}

/**
 * The fields of a flat object schema, as the Fields table makes one; undefined for any
 * other, answered as JSON.
 */
export function fieldsOf(schema: Record<string, unknown> | undefined): TaskField[] | undefined {
  const s = schema as JsonSchema | undefined
  if (s?.type !== 'object' || !s.properties) {
    return undefined
  }

  const out: TaskField[] = []

  for (const [name, raw] of Object.entries(s.properties)) {
    let prop = raw
    let optional = false

    if (raw.anyOf) {
      const real = raw.anyOf.filter((p) => p.type !== 'null')
      if (real.length !== 1 || real.length === raw.anyOf.length) {
        return undefined
      }

      optional = true
      prop = real[0]
    }

    const field: TaskField = {
      name,
      kind: 'text',
      optional,
      description: raw.description ?? prop.description,
    }

    switch (prop.type) {
      case 'string':
        if (prop.enum) {
          field.options = prop.enum.map(String)
        }
        break
      case 'number':
        field.kind = 'number'
        break
      case 'integer':
        field.kind = 'integer'
        break
      case 'boolean':
        field.kind = 'boolean'
        break
      case 'array':
        if (prop.items?.type !== 'string') {
          return undefined
        }
        field.kind = 'list'
        break
      default:
        return undefined
    }

    out.push(field)
  }

  return out
}

/** A form's first values: empty, false for true or false. */
export function emptyValues(fields: TaskField[]): Record<string, FieldValue> {
  return Object.fromEntries(fields.map((f) => [f.name, f.kind === 'boolean' ? false : '']))
}

/**
 * The answer the fields make, as the server holds it to the schema; an empty optional
 * field is null, and a number that is not one is left as typed, for the server to say so.
 */
export function answerOf(
  fields: TaskField[],
  values: Record<string, FieldValue>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {}

  for (const f of fields) {
    const v = values[f.name]

    if (f.kind === 'boolean') {
      out[f.name] = v === true
      continue
    }

    const text = typeof v === 'string' ? v.trim() : ''

    if (text === '' && f.optional) {
      out[f.name] = null
      continue
    }

    switch (f.kind) {
      case 'number':
      case 'integer': {
        const n = Number(text)
        out[f.name] = text !== '' && Number.isFinite(n) ? n : text
        break
      }
      case 'list':
        out[f.name] = text
          .split('\n')
          .map((line) => line.trim())
          .filter((line) => line !== '')
        break
      default:
        out[f.name] = text
    }
  }

  return out
}
