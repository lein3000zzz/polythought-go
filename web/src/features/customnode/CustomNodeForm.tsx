import { ChevronLeft, Plus, Trash2 } from 'lucide-react'
import { useEffect, useEffectEvent, useRef, useState } from 'react'
import { Link, useBlocker, useNavigate } from 'react-router'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { ApiError, errorMessage, type Problem } from '@/api/client'
import {
  useCreateCustomNode,
  useDeleteCustomNode,
  useUpdateCustomNode,
  type BrokenWorkflow,
  type CustomNode,
} from '@/api/customNodes'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import { BreaksDialog } from './BreaksDialog'
import { definitionOf, emptyForm, formOf, otherProblems, problemsAt, type Form } from './definition'
import { FieldRows } from './FieldRows'
import { SettingRows } from './SettingRows'
import { starter } from './starter'

// The parts of the form that show their own problems; others show at the top.
const shownParam = (param: string) =>
  ['name', 'label', 'description', 'settings', 'outputs', 'items', 'source'].includes(param) ||
  /^(settings|outputs)\.\d+(\.|$)/.test(param)

/** Makes a custom node, or edits node: its form beside its code (D57). */
export function CustomNodeForm({ node }: { node?: CustomNode }) {
  const navigate = useNavigate()
  const { data: me } = useMe()
  const codeOff = me?.mayWriteCode === false

  const [initial] = useState<Form>(() => {
    if (node) return formOf(node)

    const form = emptyForm()
    return { ...form, source: starter(form) }
  })
  const [form, setForm] = useState(initial)
  const [saved, setSaved] = useState(() => JSON.stringify(definitionOf(initial)))
  const [version, setVersion] = useState(node?.version)
  const [problems, setProblems] = useState<Problem[]>([])
  const [breaks, setBreaks] = useState<{ workflows: BrokenWorkflow[]; deleting?: boolean }>()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const leaving = useRef(false)

  const create = useCreateCustomNode()
  const update = useUpdateCustomNode(node?.id ?? '')
  const remove = useDeleteCustomNode()
  const saving = create.isPending || update.isPending
  const dirty = JSON.stringify(definitionOf(form)) !== saved

  // The code follows the form while it is the starter the form made (D59).
  function change(patch: Partial<Omit<Form, 'source'>>) {
    setForm((f) => {
      const next = { ...f, ...patch }
      return f.source === starter(f) ? { ...next, source: starter(next) } : next
    })
  }

  async function save(confirm = false) {
    if (saving || codeOff) return

    const definition = definitionOf(form)

    try {
      const n = node
        ? await update.mutateAsync({ ...definition, version: version ?? node.version, confirm })
        : await create.mutateAsync(definition)

      setSaved(JSON.stringify(definition))
      setVersion(n.version)
      setProblems([])
      setBreaks(undefined)
      toast.success(node ? 'Saved' : `Made ${n.type}`)

      if (!node) {
        leaving.current = true
        void navigate(`/nodes/${n.id}`, { replace: true })
      }
    } catch (error) {
      if (error instanceof ApiError && error.code === 'breaks_workflows') {
        setBreaks({ workflows: error.workflows })
      } else if (error instanceof ApiError && error.problems.length > 0) {
        setProblems(error.problems)
        toast.error('Not saved: see what is marked.')
      } else if (error instanceof ApiError && error.code === 'conflict') {
        toast.error('Someone saved this node since you opened it. Reload to see their version.')
      } else {
        toast.error(errorMessage(error))
      }
    }
  }

  function deleteNode(confirm = false) {
    if (!node) return

    remove.mutate(
      { id: node.id, confirm },
      {
        onSuccess: () => {
          leaving.current = true
          toast.success(`Deleted ${node.type}`)
          void navigate('/nodes')
        },
        onError: (error) => {
          if (error instanceof ApiError && error.code === 'breaks_workflows') {
            setBreaks({ workflows: error.workflows, deleting: true })
          } else {
            toast.error(errorMessage(error))
          }
        },
      },
    )
  }

  // Cmd/Ctrl+S saves, wherever the focus is.
  const onSaveKey = useEffectEvent((e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
      e.preventDefault()
      void save()
    }
  })
  useEffect(() => {
    window.addEventListener('keydown', onSaveKey)
    return () => window.removeEventListener('keydown', onSaveKey)
  }, [])

  useEffect(() => {
    if (!dirty) return

    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', warn)

    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])

  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty &&
      !leaving.current &&
      currentLocation.pathname !== nextLocation.pathname &&
      nextLocation.pathname !== '/login',
  )

  const at = (param: string) => problemsAt(problems, param)
  const general = otherProblems(problems, shownParam)

  return (
    <div className="mx-auto flex w-full max-w-7xl flex-col gap-6 p-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <Link
            to="/nodes"
            className="flex items-center text-sm text-muted-foreground hover:text-foreground"
          >
            <ChevronLeft className="size-4" />
            Nodes
          </Link>
          <h1 className="font-heading text-xl font-semibold">
            {node ? form.label || node.label : 'New custom node'}
          </h1>
          {node && <p className="font-mono text-xs text-muted-foreground">{node.type}</p>}
        </div>

        <div className="flex items-center gap-2">
          {dirty && <span className="text-sm text-muted-foreground">Unsaved changes</span>}
          {node && (
            <Button
              variant="ghost"
              className="text-destructive"
              onClick={() => setConfirmDelete(true)}
            >
              <Trash2 />
              Delete
            </Button>
          )}
          <Button onClick={() => void save()} disabled={saving || codeOff || (node && !dirty)}>
            {node ? 'Save' : 'Make the node'}
          </Button>
        </div>
      </div>

      {codeOff && (
        <Alert>
          <AlertDescription>
            An admin has turned code off for you: you can look at your custom nodes and delete them,
            not make or change them.
          </AlertDescription>
        </Alert>
      )}

      {general.length > 0 && (
        <Alert variant="destructive">
          <AlertDescription>
            <ul className="flex flex-col gap-1">
              {general.map((p, i) => (
                <li key={i}>{p.message}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}

      <fieldset disabled={codeOff} className="grid min-w-0 gap-8 lg:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-6">
          <section className="flex flex-col gap-3">
            <div className="grid gap-3 sm:grid-cols-2">
              <Text
                id="node-name"
                label="Name"
                value={form.name}
                problems={at('name')}
                readOnly={node !== undefined}
                mono
                placeholder="spread-split"
                hint={
                  node
                    ? 'Workflows name the node by it, so it stays.'
                    : `Lowercase letters, digits and hyphens. Its type: custom.${form.name || '<name>'}.`
                }
                onChange={(name) => change({ name })}
              />
              <Text
                id="node-label"
                label="Label"
                value={form.label}
                problems={at('label')}
                placeholder="Spread split"
                hint="What the node list and the canvas call it."
                onChange={(label) => change({ label })}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="node-description">Description</Label>
              <Textarea
                id="node-description"
                rows={2}
                value={form.description}
                onChange={(e) => change({ description: e.target.value })}
              />
              <Problems messages={at('description')} />
            </div>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="font-medium">Settings</h2>
            <p className="text-sm text-muted-foreground">
              What whoever places the node fills in. The code reads them as ctx.params.
            </p>
            <Problems messages={at('settings')} />
            <SettingRows
              rows={form.settings}
              problems={problems}
              onChange={(settings) => change({ settings })}
            />
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="font-medium">Outputs</h2>
            <p className="text-sm text-muted-foreground">
              Its output ports. It takes items on one input; with several outputs, run returns the
              items of each by name.
            </p>
            <Problems messages={at('outputs')} />
            <div className="flex flex-col gap-2">
              {form.outputs.map((output, i) => (
                <div key={i} className="flex flex-col gap-1">
                  <div className="flex items-center gap-2">
                    <Input
                      value={output}
                      spellCheck={false}
                      className="w-56 font-mono"
                      aria-label={`Output ${i + 1}`}
                      aria-invalid={at(`outputs.${i}`).length > 0 || undefined}
                      onChange={(e) =>
                        change({
                          outputs: form.outputs.map((o, j) => (j === i ? e.target.value : o)),
                        })
                      }
                    />
                    {form.outputs.length > 1 && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        aria-label={`Remove output ${i + 1}`}
                        onClick={() => change({ outputs: form.outputs.filter((_, j) => j !== i) })}
                      >
                        <Trash2 />
                      </Button>
                    )}
                  </div>
                  <Problems messages={at(`outputs.${i}`)} />
                </div>
              ))}
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="self-start"
                onClick={() => change({ outputs: [...form.outputs, ''] })}
              >
                <Plus />
                Add an output
              </Button>
            </div>
          </section>

          <section className="flex flex-col gap-2">
            <h2 className="font-medium">Items</h2>
            <div className="flex flex-col gap-1.5 text-sm" role="radiogroup" aria-label="Items">
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name="items"
                  checked={form.passed}
                  onChange={() => change({ passed: true })}
                />
                Passes on the items it gets, as they are
              </label>
              <label className="flex items-center gap-2">
                <input
                  type="radio"
                  name="items"
                  checked={!form.passed}
                  onChange={() => change({ passed: false })}
                />
                Makes items of its own, with these fields
              </label>
            </div>
            {!form.passed && (
              <>
                <p className="text-sm text-muted-foreground">
                  The nodes after it can pick and check these fields. Items run returns are checked
                  against them: one that does not match fails every item it got.
                </p>
                <FieldRows rows={form.fields} onChange={(fields) => change({ fields })} />
              </>
            )}
            <Problems messages={at('items')} />
          </section>
        </div>

        <section className="flex min-w-0 flex-col gap-2">
          <Label htmlFor="node-source">Code</Label>
          <p className="text-sm text-muted-foreground">
            TypeScript that exports run(items, ctx): it gets every item at once. It may import only
            from @polythought/sdk.
            {form.source === starter(form) && ' While you leave it as it is, it follows the form.'}
          </p>
          <Textarea
            id="node-source"
            rows={28}
            spellCheck={false}
            className="font-mono text-xs"
            value={form.source}
            aria-invalid={at('source').length > 0 || undefined}
            onChange={(e) => setForm((f) => ({ ...f, source: e.target.value }))}
          />
          <Problems messages={at('source')} />
        </section>
      </fieldset>

      {breaks && (
        <BreaksDialog
          workflows={breaks.workflows}
          confirm={breaks.deleting ? 'Delete anyway' : 'Save anyway'}
          onCancel={() => setBreaks(undefined)}
          onConfirm={() => (breaks.deleting ? deleteNode(true) : void save(true))}
        />
      )}

      <ConfirmDialog
        open={confirmDelete}
        title="Delete this node?"
        description="It goes for good. Workflows that use it are listed before anything goes."
        confirm="Delete"
        destructive
        onConfirm={() => {
          setConfirmDelete(false)
          deleteNode()
        }}
        onCancel={() => setConfirmDelete(false)}
      />

      <ConfirmDialog
        open={blocker.state === 'blocked'}
        title="Leave without saving?"
        description="Your changes to this node have not been saved."
        confirm="Leave"
        cancel="Stay"
        destructive
        onConfirm={() => blocker.proceed?.()}
        onCancel={() => blocker.reset?.()}
      />
    </div>
  )
}

function Text({
  id,
  label,
  value,
  problems,
  hint,
  placeholder,
  readOnly,
  mono,
  onChange,
}: {
  id: string
  label: string
  value: string
  problems: string[]
  hint?: string
  placeholder?: string
  readOnly?: boolean
  mono?: boolean
  onChange: (value: string) => void
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        value={value}
        placeholder={placeholder}
        readOnly={readOnly}
        spellCheck={false}
        className={mono ? 'font-mono' : undefined}
        aria-invalid={problems.length > 0 || undefined}
        aria-describedby={`${id}-hint`}
        onChange={(e) => onChange(e.target.value)}
      />
      {hint && (
        <p id={`${id}-hint`} className="text-xs text-muted-foreground">
          {hint}
        </p>
      )}
      <Problems messages={problems} />
    </div>
  )
}

function Problems({ messages }: { messages: string[] }) {
  return messages.map((m, i) => (
    <p key={i} role="alert" className="text-xs text-destructive">
      {m}
    </p>
  ))
}
