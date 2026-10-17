import { describe, expect, it } from 'vitest'

import { workflow } from './workflow'

describe('workflow code', () => {
  it('gives the graph: nodes as declared, connections as made', () => {
    const made = workflow('Spread watch', (w) => {
      const manual = w.trigger('manual', 'trigger.manual')
      const tight = w.node(
        'tight',
        'logic.if',
        { field: 'spread', operator: 'lt', value: '0.02' },
        { name: 'Tight spread', notes: 'Under 2 cents', continueOnFail: true },
      )
      const log = w.node('log', 'output.log', { message: 'Tight: {{ question }}' })
      const failed = w.node('failed', 'output.log', { message: 'Failed' })

      manual.to(tight)
      tight.port('true').to(log)
      tight.port('error').to(failed.input('main'))
    })

    expect(made).toEqual({
      name: 'Spread watch',
      nodes: [
        { id: 'manual', type: 'trigger.manual' },
        {
          id: 'tight',
          type: 'logic.if',
          name: 'Tight spread',
          notes: 'Under 2 cents',
          params: { field: 'spread', operator: 'lt', value: '0.02' },
          continueOnFail: true,
        },
        { id: 'log', type: 'output.log', params: { message: 'Tight: {{ question }}' } },
        { id: 'failed', type: 'output.log', params: { message: 'Failed' } },
      ],
      connections: [
        { from: 'manual', fromPort: 'main', to: 'tight', toPort: 'main' },
        { from: 'tight', fromPort: 'true', to: 'log', toPort: 'main' },
        { from: 'tight', fromPort: 'error', to: 'failed', toPort: 'main' },
      ],
    })
  })

  it('keeps settings apart from what the code does with its object later', () => {
    const settings = { message: 'first' }
    const made = workflow('w', (w) => {
      w.trigger('manual', 'trigger.manual')
      w.node('log', 'output.log', settings)
    })

    settings.message = 'changed'
    expect(made.nodes[1].params).toEqual({ message: 'first' })
  })

  it.each([
    [
      'an ID twice',
      () =>
        workflow('w', (w) => {
          w.trigger('a', 'trigger.manual')
          w.node('a', 'output.log')
        }),
      'node "a" is declared twice',
    ],
    [
      'no ID',
      () => workflow('w', (w) => void w.trigger('', 'trigger.manual')),
      'a node needs an ID',
    ],
    [
      'a node of another workflow',
      () => {
        let other: Parameters<Parameters<typeof workflow>[1]>[0] | undefined
        const theirs = workflow('theirs', (w) => {
          other = w
        })
        void theirs

        return workflow('mine', (w) => {
          const manual = w.trigger('manual', 'trigger.manual')
          manual.to(other!.node('log', 'output.log'))
        })
      },
      'manual connects to "log", which is not a node of this workflow',
    ],
  ])('refuses %s', (_, build, message) => {
    expect(build).toThrow(message)
  })
})
