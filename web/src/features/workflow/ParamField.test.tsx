import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
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
