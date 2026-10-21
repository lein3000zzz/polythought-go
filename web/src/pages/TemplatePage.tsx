import { ChevronLeft, Copy, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import {
  ApiError,
  errorMessage,
  type NodeType,
  type Schemas,
  type WorkflowNode,
} from '@/api/client'
import { useDeleteTemplate, useTemplate, useUseTemplate, type Template } from '@/api/library'
import { useNodeTypes } from '@/api/workflows'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Prose } from '@/components/Prose'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { Skeleton } from '@/components/ui/skeleton'
import { ParamField } from '@/features/workflow/ParamField'
import { textOf } from '@/features/workflow/params'
import { fieldMessage } from '@/features/workflow/problems'
import { TemplateCanvas } from '@/features/workflow/TemplateCanvas'

type TemplateCustomNode = Schemas['TemplateCustomNode']

/** One library entry: what it does, what it looks like, and a way to start from it. */
export function TemplatePage() {
  const { id = '' } = useParams()
  const template = useTemplate(id)
  const nodeTypes = useNodeTypes()
  const { data: me } = useMe()
  const navigate = useNavigate()
  const remove = useDeleteTemplate()
  const [selected, setSelected] = useState<string>()
  const [using, setUsing] = useState(false)
  const [deleting, setDeleting] = useState(false)

  const back = (
    <Link
      to="/library"
      className="flex items-center text-sm text-muted-foreground hover:text-foreground"
    >
      <ChevronLeft className="size-4" />
      Library
    </Link>
  )

  if (template.isError || nodeTypes.isError) {
    const error = template.error ?? nodeTypes.error

    return (
      <div className="mx-auto flex max-w-xl flex-col gap-4 p-6">
        {back}
        {error instanceof ApiError && error.status === 404 ? (
          <p className="text-sm">This is not in the library, or no longer.</p>
        ) : (
          <ErrorAlert error={error} />
        )}
      </div>
    )
  }

  if (template.isPending || nodeTypes.isPending) {
    return (
      <div className="flex flex-col gap-4 p-6">
        {back}
        <Skeleton className="h-96" />
      </div>
    )
  }

  const t = template.data
  const brought = t.customNodes ?? []
  // The custom nodes it brings are drawn as it has them, not as yours of the same name.
  const types = [
    ...brought.map((c) => c.nodeType),
    ...nodeTypes.data.filter((x) => !brought.some((c) => c.type === x.name)),
  ]
  const node = t.nodes?.find((n) => n.id === selected)
  const canDelete = !t.builtIn && (t.mine || me?.role === 'admin')

  return (
    <div className="flex h-full flex-col">
      <header className="flex items-center gap-3 border-b px-4 py-2">
        {back}
        <span className="text-muted-foreground">/</span>
        <h1 className="font-heading font-semibold">{t.name}</h1>
        <Badge variant="secondary">
          {t.builtIn ? 'Scenario' : t.mine ? 'Shared by you' : 'Shared by a member'}
        </Badge>

        <div className="ml-auto flex items-center gap-2">
          {canDelete && (
            <Button variant="ghost" onClick={() => setDeleting(true)}>
              <Trash2 />
              Remove from library
            </Button>
          )}
          <Button onClick={() => setUsing(true)}>
            <Copy />
            Use this
          </Button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        <aside className="w-96 shrink-0 overflow-y-auto border-r p-5" aria-label="About">
          {node ? (
            <NodeAbout
              node={node}
              type={types.find((x) => x.name === node.type)}
              brought={brought.find((c) => c.type === node.type)}
              onBack={() => setSelected(undefined)}
            />
          ) : (
            <div className="flex flex-col gap-5">
              {t.summary && <p className="font-medium">{t.summary}</p>}
              {t.description && <Prose text={t.description} />}
              {t.inputs.length > 0 && (
                <div className="rounded-lg bg-muted/60 p-3 text-sm">
                  <p className="font-medium">You fill in</p>
                  <ul className="mt-1 list-disc pl-5">
                    {t.inputs.map((i) => (
                      <li key={`${i.nodeId}.${i.param.name}`}>
                        {i.param.label ?? i.param.name}
                        {!i.param.required && (
                          <span className="text-muted-foreground"> (optional)</span>
                        )}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {brought.length > 0 && <Brings nodes={brought} />}
              <p className="text-xs text-muted-foreground">
                Click a node on the right to see what it does and how it is set.
              </p>
            </div>
          )}
        </aside>

        <div className="min-w-0 flex-1">
          <TemplateCanvas template={t} types={types} onSelect={setSelected} />
        </div>
      </div>

      <UseDialog template={t} types={types} open={using} onClose={() => setUsing(false)} />

      <ConfirmDialog
        open={deleting}
        title={`Remove “${t.name}” from the library?`}
        description="Workflows people already made from it stay theirs."
        confirm="Remove"
        destructive
        onCancel={() => setDeleting(false)}
        onConfirm={() =>
          remove.mutate(t.id, {
            onSuccess: () => {
              toast.success('Removed from the library')
              void navigate('/library')
            },
            onError: (error) => toast.error(errorMessage(error)),
          })
        }
      />
    </div>
  )
}

// Brings shows the custom nodes a shared workflow brings, code included, before anyone
// uses it (D62): the code runs for whoever does.
function Brings({ nodes }: { nodes: TemplateCustomNode[] }) {
  return (
    <section aria-label="Custom nodes it brings" className="flex flex-col gap-2 text-sm">
      <p className="font-medium">Custom nodes it brings</p>
      <p className="text-xs text-muted-foreground">
        Using it copies them into your Nodes, under a new name if one of yours by that name differs;
        one of yours just like it is used as it is. Their code runs for you, so read it first.
      </p>
      {nodes.map((n) => (
        <CustomNodeAbout key={n.type} node={n} />
      ))}
    </section>
  )
}

function CustomNodeAbout({ node, open }: { node: TemplateCustomNode; open?: boolean }) {
  return (
    <details className="rounded-lg border p-3" open={open}>
      <summary className="cursor-pointer">
        <span className="font-medium">{node.label}</span>{' '}
        <span className="font-mono text-xs text-muted-foreground">{node.type}</span>
      </summary>
      <div className="mt-2 flex flex-col gap-2 text-xs">
        {node.description && <p>{node.description}</p>}
        <p>
          <span className="text-muted-foreground">Settings: </span>
          {node.settings.length > 0
            ? node.settings.map((s) => `${s.name} (${s.kind})`).join(', ')
            : 'none'}
        </p>
        <p>
          <span className="text-muted-foreground">Outputs: </span>
          {node.outputs.join(', ')}
        </p>
        <p>
          <span className="text-muted-foreground">Items: </span>
          {node.items.passed
            ? 'the ones it gets, passed on'
            : `its own, with ${node.items.fields.map((f) => f.name).join(', ') || 'no fields'}`}
        </p>
        <pre
          aria-label={`Code of ${node.label}`}
          className="max-h-80 overflow-auto rounded-md bg-muted p-2 font-mono"
        >
          {node.source}
        </pre>
      </div>
    </details>
  )
}

function NodeAbout({
  node,
  type,
  brought,
  onBack,
}: {
  node: WorkflowNode
  type?: NodeType
  brought?: TemplateCustomNode
  onBack: () => void
}) {
  const params = node.params ?? {}

  return (
    <div className="flex flex-col gap-4 text-sm">
      <button
        type="button"
        onClick={onBack}
        className="self-start text-xs text-muted-foreground hover:text-foreground"
      >
        ← About this workflow
      </button>
      <div>
        <h2 className="font-medium">{node.name || type?.label || node.type}</h2>
        <p className="text-xs text-muted-foreground">{type?.label ?? node.type}</p>
      </div>
      {node.notes && <p className="whitespace-pre-line">{node.notes}</p>}
      {type?.description && <p className="text-muted-foreground">{type.description}</p>}
      {brought && <CustomNodeAbout node={brought} open />}

      {type && type.params.length > 0 && (
        <dl className="flex flex-col gap-2">
          {type.params.map((p) => (
            <div key={p.name}>
              <dt className="text-xs text-muted-foreground">{p.label ?? p.name}</dt>
              <dd className="font-mono text-xs break-words">
                {p.name in params
                  ? textOf(params[p.name]).replace(/\n/g, ', ')
                  : p.default !== undefined
                    ? `${textOf(p.default)} (default)`
                    : p.required
                      ? 'You fill this in'
                      : '—'}
              </dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  )
}

type UseDialogProps = {
  template: Template
  types: readonly NodeType[]
  open: boolean
  onClose: () => void
}

/** Asks for a name and the template's inputs, then makes the workflow and opens it. */
function UseDialog({ template: t, types, open, onClose }: UseDialogProps) {
  const navigate = useNavigate()
  const use = useUseTemplate(t.id)
  const [name, setName] = useState(t.name)
  const [values, setValues] = useState<Record<string, Record<string, unknown>>>({})

  const problems = use.error instanceof ApiError ? use.error.problems : []
  const problemFor = (nodeId: string, param: string) =>
    problems
      .filter((p) => p.nodeId === nodeId && p.param === param)
      .map(fieldMessage)
      .join(' ')
  const generalError = use.isError && !problems.some((p) => p.param) ? use.error : undefined

  const nodeLabel = (nodeId: string) => {
    const n = t.nodes?.find((x) => x.id === nodeId)
    return n?.name || types.find((x) => x.name === n?.type)?.label || nodeId
  }

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Make it yours</DialogTitle>
          <DialogDescription>
            Creates a workflow of your own from “{t.name}”. It starts switched off; change anything
            you like before you turn it on.
          </DialogDescription>
        </DialogHeader>

        <form
          id="use-template"
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            use.mutate(
              { name: name.trim() || t.name, values },
              {
                onSuccess: (w) => {
                  toast.success('Workflow created. Press Run to try it.')
                  void navigate(`/workflows/${w.id}`)
                },
              },
            )
          }}
        >
          {generalError && <ErrorAlert error={generalError} />}

          <div className="flex flex-col gap-2">
            <Label htmlFor="use-name">Name</Label>
            <Input id="use-name" value={name} onChange={(e) => setName(e.target.value)} required />
          </div>

          {t.inputs.map((input) => (
            <div key={`${input.nodeId}.${input.param.name}`} className="flex flex-col gap-1">
              <p className="text-xs text-muted-foreground">For {nodeLabel(input.nodeId)}</p>
              <ParamField
                scope={`use-${input.nodeId}`}
                spec={input.param}
                value={values[input.nodeId]?.[input.param.name]}
                problem={problemFor(input.nodeId, input.param.name)}
                // A model's input suggests those of the key chosen beside it.
                modelsKey={
                  input.param.modelsOf
                    ? keyOf(values[input.nodeId]?.[input.param.modelsOf])
                    : undefined
                }
                onChange={(v) =>
                  setValues((current) => ({
                    ...current,
                    [input.nodeId]: { ...current[input.nodeId], [input.param.name]: v },
                  }))
                }
              />
            </div>
          ))}
        </form>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="use-template" disabled={use.isPending}>
            Create workflow
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function keyOf(v: unknown): string | undefined {
  return typeof v === 'string' && v !== '' ? v : undefined
}
