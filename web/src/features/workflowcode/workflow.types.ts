// What the types of @polythought/workflow allow and refuse, checked by tsc (D73). Not run:
// each @ts-expect-error fails the check when its line stops being an error.
import { workflow } from './workflow'

export default workflow('Spread watch', (w) => {
  const manual = w.trigger('manual', 'trigger.manual')
  const tight = w.node(
    'tight',
    'logic.if',
    { field: 'spread', operator: 'lt', value: '0.02' },
    { name: 'Tight spread', continueOnFail: true },
  )
  const log = w.node('log', 'output.log', { message: 'Tight: {{ question }}' })
  const quiet = w.node('quiet', 'output.log') // no settings needed
  const ask = w.node('ask', 'telegram.ask', { message: 'Buy?', buttons: ['Yes', 'No'] })
  const top = w.node('top', 'transform.top', { field: 'liquidity', limit: '{{ params.max }}' })

  manual.to(tight)
  tight.port('true').to(log)
  tight.port('error').to(quiet)
  ask.port('Yes').to(top) // a port its buttons make
  ask.port('timeout').to(quiet)

  // @ts-expect-error: If has no output "ture"
  tight.port('ture').to(log)
  // @ts-expect-error: a trigger has no inputs
  manual.input('main')
  // @ts-expect-error: If's field and value are required
  w.node('bare', 'logic.if')
  // @ts-expect-error: an operator If does not take
  w.node('odd', 'logic.if', { field: 'spread', operator: 'about', value: '1' })
  // @ts-expect-error: not a trigger
  w.trigger('t', 'output.log')
  // @ts-expect-error: a trigger, not a step
  w.node('n', 'trigger.manual')
  // @ts-expect-error: no such node type
  w.node('x', 'logic.nope')
  // @ts-expect-error: options are a name, notes and continueOnFail
  w.node('y', 'output.log', {}, { colour: 'red' })
})
