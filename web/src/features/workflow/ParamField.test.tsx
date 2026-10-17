import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'

import { createQueryClient } from '@/api/queryClient'
import type { ParamSpec } from '@/api/client'
import { server } from '@/test/server'

import { ParamField } from './ParamField'

const spec: ParamSpec = {
  name: 'credential',
  label: 'Credential',
  kind: 'credential',
  credentialType: 'http.header',
  required: false,
}

function renderField(value: unknown, onChange = vi.fn()) {
  const queryClient = createQueryClient()
  queryClient.setDefaultOptions({ queries: { retry: false } })

  render(
    <QueryClientProvider client={queryClient}>
      <ParamField spec={spec} scope="use-hook" value={value} onChange={onChange} />
    </QueryClientProvider>,
  )

  return { onChange, user: userEvent.setup() }
}

function vault(credentials: { id: string; type: string; name: string }[]) {
  server.use(
    http.get('/api/v1/credential-types', () =>
      HttpResponse.json({
        credentialTypes: [
          { name: 'http.header', label: 'HTTP header', fields: [], testable: false },
        ],
      }),
    ),
    http.get('/api/v1/credentials', () =>
      HttpResponse.json({
        credentials: credentials.map((c) => ({
          ...c,
          values: {},
          secrets: [],
          createdAt: '2026-10-01T10:00:00Z',
          updatedAt: '2026-10-01T10:00:00Z',
        })),
      }),
    ),
  )
}

describe('credential field', () => {
  it('offers the user’s credentials of the kind asked for', async () => {
    vault([
      { id: 'c-1', type: 'http.header', name: 'Relay' },
      { id: 'c-2', type: 'test.api', name: 'Other kind' },
    ])
    const { user, onChange } = renderField(undefined)

    const select = await screen.findByRole('combobox', { name: 'Credential' })
    await screen.findByRole('option', { name: 'Relay' })
    expect(screen.getByRole('option', { name: 'None' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Other kind' })).not.toBeInTheDocument()

    await user.selectOptions(select, 'c-1')
    expect(onChange).toHaveBeenLastCalledWith('c-1')

    await user.selectOptions(select, '')
    expect(onChange).toHaveBeenLastCalledWith(undefined)
  })

  it('says how to get one, and shows a deleted one as missing', async () => {
    vault([])
    renderField('c-9')

    expect(await screen.findByText(/You have no HTTP header credentials yet/)).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Missing credential (c-9)' })).toBeInTheDocument()
  })
})

describe('expressions in switches and dropdowns', () => {
  const choose = (extra: Partial<ParamSpec>, value: unknown, expressions = true) => {
    const onChange = vi.fn()
    render(
      <ParamField
        spec={{ name: 'mode', label: 'Mode', kind: 'string', required: false, ...extra }}
        value={value}
        onChange={onChange}
        expressions={expressions}
      />,
    )

    return { onChange, user: userEvent.setup() }
  }

  it('turns a dropdown into text for one, and back', async () => {
    const { onChange, user } = choose({ options: ['fast', 'slow'] }, 'fast')
    expect(screen.getByRole('combobox')).toHaveValue('fast')

    await user.click(screen.getByRole('button', { name: 'Use an expression for Mode' }))
    expect(onChange).toHaveBeenLastCalledWith(undefined)

    // Pasted: user-event reads {{ as a typed {.
    await user.click(screen.getByRole('textbox'))
    await user.paste("{{ params.urgent ? 'fast' : 'slow' }}")
    expect(onChange).toHaveBeenLastCalledWith("{{ params.urgent ? 'fast' : 'slow' }}")

    await user.click(screen.getByRole('button', { name: 'Use an expression for Mode' }))
    expect(screen.getByRole('combobox')).toBeInTheDocument()
    expect(onChange).toHaveBeenLastCalledWith(undefined)
  })

  it('starts as text when the value is one', () => {
    choose({ kind: 'bool' }, '{{ open }}')

    expect(screen.getByRole('textbox')).toHaveValue('{{ open }}')
    expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Use an expression for Mode' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
  })

  it('offers none where the setting takes none', () => {
    choose({ kind: 'bool' }, true, false)

    expect(screen.getByRole('switch')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Use an expression/ })).not.toBeInTheDocument()
  })
})

describe('table field', () => {
  const traders: ParamSpec = {
    name: 'traders',
    label: 'Traders',
    kind: 'table',
    required: false,
    columns: [
      { name: 'label', label: 'Label', kind: 'string', required: true },
      { name: 'address', label: 'Address', kind: 'address', required: true },
      { name: 'on', label: 'On', kind: 'bool', required: false, default: true },
      { name: 'side', label: 'Side', kind: 'string', required: false, options: ['buy', 'sell'] },
      { name: 'size', label: 'Size', kind: 'decimal', required: false },
    ],
  }

  const alice = '0x5f3c00000000000000000000000000000000a1b2'
  const bob = '0x91d000000000000000000000000000000000c7e4'
  const carol = '0x0b7a000000000000000000000000000000009f00'

  // table renders the field with its value kept, as the node panel keeps it.
  function table(initial: unknown, expressions = false) {
    const changes: unknown[] = []

    function Harness() {
      const [value, setValue] = useState(initial)

      return (
        <ParamField
          spec={traders}
          value={value}
          expressions={expressions}
          onChange={(v) => {
            changes.push(v)
            setValue(v)
          }}
        />
      )
    }

    render(<Harness />)

    return { changes, user: userEvent.setup() }
  }

  it('sums the rows up, and edits them in a dialog', async () => {
    const { changes, user } = table([
      { label: 'Alice', address: alice },
      { label: 'Bob', address: bob, on: false },
    ])

    expect(screen.getByText('2 rows: Alice, Bob')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Edit traders' }))

    const dialog = within(screen.getByRole('dialog', { name: 'Traders' }))
    expect(dialog.getByRole('textbox', { name: 'Label, row 1' })).toHaveValue('Alice')
    expect(dialog.getByRole('checkbox', { name: 'On, row 1' })).toBeChecked() // its default
    expect(dialog.getByRole('checkbox', { name: 'On, row 2' })).not.toBeChecked()

    await user.click(dialog.getByRole('button', { name: 'Add a row' }))
    await user.type(dialog.getByRole('textbox', { name: 'Label, row 3' }), 'Carol')
    await user.type(dialog.getByRole('textbox', { name: 'Address, row 3' }), '0x12')
    expect(dialog.getByRole('alert')).toHaveTextContent(
      'Row 3, Address: Enter an address: 0x and 40 hex digits',
    )

    await user.clear(dialog.getByRole('textbox', { name: 'Address, row 3' }))
    await user.type(dialog.getByRole('textbox', { name: 'Address, row 3' }), carol)
    expect(dialog.queryByRole('alert')).not.toBeInTheDocument()

    await user.selectOptions(dialog.getByRole('combobox', { name: 'Side, row 3' }), 'sell')
    await user.type(dialog.getByRole('textbox', { name: 'Size, row 3' }), '0.5')
    await user.click(dialog.getByRole('button', { name: 'Move row 3 up' }))
    await user.click(dialog.getByRole('button', { name: 'Remove row 1' }))

    // What was typed moves with its row.
    expect(dialog.getByRole('textbox', { name: 'Label, row 1' })).toHaveValue('Carol')
    expect(changes.at(-1)).toEqual([
      { label: 'Carol', address: carol, side: 'sell', size: '0.5' },
      { label: 'Bob', address: bob, on: false },
    ])

    await user.click(dialog.getByRole('button', { name: 'Done' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByText('2 rows: Carol, Bob')).toBeInTheDocument()
  })

  it('points at required cells left empty, and keeps no rows as unset', async () => {
    const { changes, user } = table(undefined)

    expect(screen.getByText('No rows yet')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Edit traders' }))

    const dialog = within(screen.getByRole('dialog'))
    await user.click(dialog.getByRole('button', { name: 'Add a row' }))
    expect(dialog.getByRole('alert')).toHaveTextContent('Row 1: Label is required')
    expect(dialog.getByRole('alert')).toHaveTextContent('Row 1: Address is required')

    await user.click(dialog.getByRole('button', { name: 'Remove row 1' }))
    expect(changes.at(-1)).toBeUndefined()
  })

  it('takes the whole table as an expression', async () => {
    const { changes, user } = table([{ label: 'Alice', address: alice }], true)

    await user.click(screen.getByRole('button', { name: 'Use an expression for Traders' }))
    await user.click(screen.getByRole('textbox'))
    await user.paste('{{ params.traders }}')

    expect(changes.at(-1)).toBe('{{ params.traders }}')
    expect(screen.queryByRole('button', { name: 'Edit traders' })).not.toBeInTheDocument()
  })
})
