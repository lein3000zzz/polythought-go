import { describe, expect, it } from 'vitest'

import { answerOf, emptyValues, fieldsOf } from './taskForm'

// As the Fields table makes one (D255): closed, every field required, optional ones null.
const fieldsSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    pick: { type: 'string', enum: ['buy', 'sell', 'wait'], description: 'What to do' },
    size: { type: 'number' },
    count: { anyOf: [{ type: 'integer' }, { type: 'null' }] },
    sure: { type: 'boolean' },
    reasons: { type: 'array', items: { type: 'string' } },
  },
  required: ['pick', 'size', 'count', 'sure', 'reasons'],
}

describe('the form a task is answered with (D413)', () => {
  it('reads the fields of a schema the Fields table makes', () => {
    expect(fieldsOf(fieldsSchema)).toEqual([
      {
        name: 'pick',
        kind: 'text',
        options: ['buy', 'sell', 'wait'],
        optional: false,
        description: 'What to do',
      },
      { name: 'size', kind: 'number', optional: false },
      { name: 'count', kind: 'integer', optional: true },
      { name: 'sure', kind: 'boolean', optional: false },
      { name: 'reasons', kind: 'list', optional: false },
    ])
  })

  it('answers any other as JSON', () => {
    expect(fieldsOf(undefined)).toBeUndefined()
    expect(fieldsOf({ type: 'array', items: { type: 'string' } })).toBeUndefined()
    expect(
      fieldsOf({ type: 'object', properties: { nested: { type: 'object', properties: {} } } }),
    ).toBeUndefined()
    expect(
      fieldsOf({
        type: 'object',
        properties: { many: { type: 'array', items: { type: 'number' } } },
      }),
    ).toBeUndefined()
  })

  it('makes the answer the server holds to the schema', () => {
    const fields = fieldsOf(fieldsSchema)!
    expect(emptyValues(fields)).toEqual({ pick: '', size: '', count: '', sure: false, reasons: '' })

    expect(
      answerOf(fields, {
        pick: 'buy',
        size: ' 2.5 ',
        count: '',
        sure: true,
        reasons: 'cheap\n\n late ',
      }),
    ).toEqual({ pick: 'buy', size: 2.5, count: null, sure: true, reasons: ['cheap', 'late'] })

    expect(
      answerOf(fields, { pick: '', size: 'lots', count: '3', sure: false, reasons: '' }),
    ).toEqual({
      pick: '',
      size: 'lots', // as typed: the server says it is no number
      count: 3,
      sure: false,
      reasons: [],
    })
  })
})
