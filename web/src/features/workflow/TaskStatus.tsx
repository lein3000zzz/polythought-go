import { useState } from 'react'
import { toast } from 'sonner'

import { errorMessage } from '@/api/client'
import {
  type ExecutionTask,
  useAnswerTask,
  useExecutionTask,
  useHandBackTask,
} from '@/api/executions'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { formatDateTime, formatTime } from '@/lib/format'

import { answerOf, emptyValues, fieldsOf, type FieldValue, type TaskField } from './taskForm'

/** The node type that hands its owner's agents a task (D386). */
export const localAgentType = 'ai.localAgent'

type Props = {
  workflowId: string
  executionId: string
}

/**
 * A run waiting on a Local agent node (D413): which of your agents has its task and until
 * when, or that it waits for one to take it; handing it back, or answering it yourself.
 */
export function TaskStatus({ workflowId, executionId }: Props) {
  const task = useExecutionTask(workflowId, executionId)
  const handBack = useHandBackTask(workflowId, executionId)
  const [answering, setAnswering] = useState(false)

  if (!task.data) {
    return null
  }

  const t = task.data

  return (
    <>
      <span className="text-muted-foreground">
        {t.heldBy
          ? `${t.heldBy} has the task${t.heldUntil ? ` until ${formatTime(t.heldUntil)}` : ''}`
          : 'Waiting for one of your agents to take the task'}
        {`; the run waits until ${formatDateTime(t.until)}`}
      </span>
      {t.heldBy && (
        <Button
          variant="outline"
          size="sm"
          disabled={handBack.isPending}
          onClick={() =>
            handBack.mutate(undefined, {
              onSuccess: () => toast.success('Handed back: another of your agents can take it'),
              onError: (error) => toast.error(errorMessage(error)),
            })
          }
        >
          Hand back
        </Button>
      )}
      <Button variant="outline" size="sm" onClick={() => setAnswering(true)}>
        Answer it
      </Button>
      {answering && (
        <AnswerTaskDialog
          workflowId={workflowId}
          executionId={executionId}
          task={t}
          onClose={() => setAnswering(false)}
        />
      )}
    </>
  )
}

type DialogProps = Props & { task: ExecutionTask; onClose: () => void }

/** Answers a task yourself, held to its shape as an agent's answer is. */
function AnswerTaskDialog({ workflowId, executionId, task, onClose }: DialogProps) {
  const answer = useAnswerTask(workflowId, executionId)
  const fields = task.answerAs === 'text' ? undefined : fieldsOf(task.schema)
  const count = task.call === 'each' ? task.items.length : 1

  const [texts, setTexts] = useState<string[]>(() => Array<string>(count).fill(''))
  const [values, setValues] = useState<Record<string, FieldValue>[]>(() =>
    Array.from({ length: count }, () => emptyValues(fields ?? [])),
  )

  const answers = Array.from({ length: count }, (_, i) =>
    fields ? answerOf(fields, values[i]) : texts[i],
  )

  const send = () =>
    answer.mutate(task.call === 'each' ? { answers } : { answer: answers[0] }, {
      onSuccess: () => {
        toast.success('Answered: the run carries on')
        onClose()
      },
      onError: (error) => toast.error(errorMessage(error)),
    })

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Answer the task</DialogTitle>
          <DialogDescription>
            Your answer goes on as an agent&apos;s would, held to the same shape
            {task.call === 'each' ? ', one for each item' : ''}.
          </DialogDescription>
        </DialogHeader>

        {task.call === 'together' && <Asked prompt={task.prompt} />}

        {Array.from({ length: count }, (_, i) => (
          <section key={i} className="flex flex-col gap-2 rounded-md border p-3">
            {task.call === 'each' ? (
              <>
                <h3 className="text-sm font-medium">Item {i + 1}</h3>
                <Asked prompt={task.items[i]?.prompt} item={task.items[i]?.item} />
              </>
            ) : (
              task.items.length > 0 && <Asked items={task.items.map((it) => it.item)} />
            )}

            {fields ? (
              fields.map((f) => (
                <FieldInput
                  key={f.name}
                  id={`task-${i}-${f.name}`}
                  field={f}
                  value={values[i][f.name]}
                  onChange={(v) =>
                    setValues((all) =>
                      all.map((row, j) => (j === i ? { ...row, [f.name]: v } : row)),
                    )
                  }
                />
              ))
            ) : (
              <div className="flex flex-col gap-1">
                <Label htmlFor={`task-${i}`}>
                  {task.answerAs === 'text' ? 'Answer' : 'Answer, as JSON keeping to the schema'}
                </Label>
                <Textarea
                  id={`task-${i}`}
                  rows={4}
                  className={task.answerAs === 'text' ? undefined : 'font-mono text-xs'}
                  value={texts[i]}
                  onChange={(e) =>
                    setTexts((all) => all.map((v, j) => (j === i ? e.target.value : v)))
                  }
                />
              </div>
            )}
          </section>
        ))}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={answer.isPending} onClick={send}>
            Send answer
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type AskedProps = { prompt?: string; item?: Record<string, unknown>; items?: unknown[] }

/** What a task asks: its prompt, and the item or items it is about. */
function Asked({ prompt, item, items }: AskedProps) {
  return (
    <div className="flex flex-col gap-1 text-sm">
      {prompt && <p className="whitespace-pre-wrap">{prompt}</p>}
      {(item ?? items) && (
        <details>
          <summary className="cursor-pointer text-xs text-muted-foreground">
            {items ? `The items (${items.length})` : 'The item'}
          </summary>
          <pre className="mt-1 max-h-48 overflow-auto rounded-md bg-muted p-2 font-mono text-xs">
            {JSON.stringify(item ?? items, null, 2)}
          </pre>
        </details>
      )}
    </div>
  )
}

type FieldProps = {
  id: string
  field: TaskField
  value: FieldValue
  onChange: (v: FieldValue) => void
}

function FieldInput({ id, field: f, value, onChange }: FieldProps) {
  const label = `${f.name}${f.optional ? ' (optional)' : ''}`

  if (f.kind === 'boolean') {
    return (
      <div className="flex items-center gap-2">
        <Checkbox
          id={id}
          checked={value === true}
          onCheckedChange={(on) => onChange(on === true)}
        />
        <Label htmlFor={id}>{label}</Label>
        {f.description && <span className="text-xs text-muted-foreground">{f.description}</span>}
      </div>
    )
  }

  const text = typeof value === 'string' ? value : ''

  return (
    <div className="flex flex-col gap-1">
      <Label htmlFor={id}>{label}</Label>
      {f.description && <p className="text-xs text-muted-foreground">{f.description}</p>}
      {f.options ? (
        <NativeSelect id={id} value={text} onChange={(e) => onChange(e.target.value)}>
          <NativeSelectOption value="">Pick one</NativeSelectOption>
          {f.options.map((o) => (
            <NativeSelectOption key={o} value={o}>
              {o}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      ) : f.kind === 'list' ? (
        <Textarea
          id={id}
          rows={3}
          placeholder="One per line"
          value={text}
          onChange={(e) => onChange(e.target.value)}
        />
      ) : (
        <Input
          id={id}
          inputMode={f.kind === 'text' ? undefined : 'decimal'}
          value={text}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
    </div>
  )
}
