import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'

import type { NodeType } from '@/api/client'

import { Palette } from './Palette'

const node = (overrides: Partial<NodeType> & Pick<NodeType, 'name'>): NodeType => ({
  label: overrides.name,
  trigger: false,
  inputs: ['main'],
  outputs: ['main'],
  params: [],
  code: false,
  ...overrides,
})

const types = [
  node({ name: 'trigger.manual', label: 'Manual', trigger: true, inputs: [] }),
  node({ name: 'output.log', label: 'Log' }),
  node({
    name: 'plugin.weather.forecast',
    label: 'Forecast',
    plugin: 'weather',
    pluginName: 'Weather',
  }),
  node({
    name: 'plugin.weather.bet',
    label: 'Bet on rain',
    plugin: 'weather',
    pluginName: 'Weather',
    trades: true,
  }),
  node({
    name: 'plugin.weather.storm',
    label: 'Storm',
    plugin: 'weather',
    pluginName: 'Weather',
    trigger: true,
    inputs: [],
  }),
]

function palette() {
  const onAdd = vi.fn()
  render(
    <MemoryRouter>
      <Palette types={types} onAdd={onAdd} />
    </MemoryRouter>,
  )

  return { onAdd, user: userEvent.setup() }
}

describe('the node list', () => {
  it("keeps plugins' nodes, triggers too, in one Plugins group, each saying whose (D330)", () => {
    palette()

    const sections = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)
    expect(sections).toEqual(['Triggers', 'Custom', 'Output', 'Plugins']) // Custom always (D57)

    const plugins = within(screen.getByRole('heading', { name: 'Plugins' }).closest('section')!)
    expect(plugins.getByText('Forecast')).toBeInTheDocument()
    expect(plugins.getByText('Storm')).toBeInTheDocument()
    expect(plugins.getAllByText('From Weather')).toHaveLength(3)

    const bet = plugins.getByText('Bet on rain').closest('button')!
    expect(within(bet).getByText('Trades')).toBeInTheDocument()
    expect(
      within(plugins.getByText('Forecast').closest('button')!).queryByText('Trades'),
    ).not.toBeInTheDocument()
  })

  it("finds a plugin's nodes by its name", async () => {
    const { user } = palette()

    await user.type(screen.getByLabelText('Search nodes'), 'weather')

    const nav = within(screen.getByRole('navigation', { name: 'Node types' }))
    expect(nav.getByText('Forecast')).toBeInTheDocument()
    expect(nav.queryByText('Log')).not.toBeInTheDocument()
  })
})
