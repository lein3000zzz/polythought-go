import '@xyflow/react/dist/style.css'

import {
  Background,
  Controls,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type IsValidConnection,
  type NodeChange,
  type OnConnect,
  type XYPosition,
} from '@xyflow/react'
import { ChevronLeft, EllipsisVertical, Trash2 } from 'lucide-react'
import { useEffect, useEffectEvent, useMemo, useRef, useState } from 'react'
import { Link, useBlocker, useNavigate } from 'react-router'
import { toast } from 'sonner'

import { ApiError, errorMessage, type NodeType, type Problem, type Workflow } from '@/api/client'
import {
  useCreateWorkflow,
  useDeleteWorkflow,
  useSetActive,
  useUpdateWorkflow,
} from '@/api/workflows'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'

import {
  defaultPort,
  edgeOf,
  errorPort,
  linkProblem,
  newNode,
  sameDefinition,
  toDefinition,
  toFlow,
  type Definition,
  type FlowEdge,
  type FlowNode,
  type NodeSpec,
} from './graph'
import { NodePanel, WorkflowPanel } from './NodePanel'
import { nodeTypeMime, Palette } from './Palette'
import { ProblemsContext } from './problems'
import { WorkflowNodeView } from './WorkflowNodeView'

// Defined once: React Flow re-mounts every node when this object changes identity.
const nodeTypes = { workflow: WorkflowNodeView }

// A new workflow starts with this trigger, the one "Run" uses.
const manualTrigger = 'trigger.manual'

// Fitting never zooms in past 100%, so a small workflow is not blown up.
const fitViewOptions = { padding: 0.2, maxZoom: 1 }

// New nodes are placed by their top-left corner; these centre them on the target point.
const nodeOffset = { x: 120, y: 40 }

type Props = {
  workflow?: Workflow // undefined for a workflow not saved yet
  types: NodeType[]
  onReload: () => void // fetch the latest version and start over from it
}

export function WorkflowEditor(props: Props) {
  return (
    <ReactFlowProvider>
      <Editor {...props} />
    </ReactFlowProvider>
  )
}

function Editor({ workflow, types, onReload }: Props) {
  const navigate = useNavigate()
  const { screenToFlowPosition, deleteElements } = useReactFlow<FlowNode, FlowEdge>()
  const canvas = useRef<HTMLDivElement>(null)

  const [initial] = useState(() => {
    const name = workflow?.name ?? 'Untitled workflow'
    const flow = workflow ? toFlow(workflow, types) : starter(types)

    return { ...flow, definition: toDefinition(name, flow.nodes, flow.edges) }
  })

  const [name, setName] = useState(initial.definition.name)
  const [nodes, setNodes, onNodesChange] = useNodesState<FlowNode>(initial.nodes)
  const [edges, setEdges, onEdgesChange] = useEdgesState<FlowEdge>(initial.edges)
  // What the server has, to tell unsaved changes. Opening a workflow counts as saved even
  // when it was laid out automatically: the layout is saved with the next real change.
  const [saved, setSaved] = useState<Definition | undefined>(workflow && initial.definition)
  // The version these edits build on. It moves only when this editor saves, so a newer
  // version loaded in the background is reported, never silently overwritten.
  const [version, setVersion] = useState(workflow?.version)
  const [problems, setProblems] = useState<Problem[]>([])
  const [conflict, setConflict] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const leaving = useRef(false) // set before navigating away on purpose

  const definition = useMemo(() => toDefinition(name, nodes, edges), [name, nodes, edges])
  const dirty = !sameDefinition(definition, saved ?? initial.definition)
  const selected = nodes.filter((n) => n.selected)
  const selectedNode = selected.length === 1 ? selected[0] : undefined
  const newer = workflow !== undefined && version !== undefined && workflow.version > version

  const create = useCreateWorkflow()
  const update = useUpdateWorkflow(workflow?.id ?? '')
  const saving = create.isPending || update.isPending

  function save() {
    if (saving) return

    const sent = definition
    const done = (w: Workflow) => {
      setSaved({ ...sent, name: w.name })
      setName((current) => (current === sent.name ? w.name : current)) // the server trims it
      setVersion(w.version)
      setProblems([])
    }
    const failed = (error: Error) => {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true)
      } else if (error instanceof ApiError && error.problems.length > 0) {
        setProblems(error.problems)
        toast.error('Not saved: the workflow has problems, marked in red.')
      } else {
        toast.error(errorMessage(error))
      }
    }

    if (!workflow) {
      create.mutate(sent, {
        onSuccess: (w) => {
          done(w)
          leaving.current = true
          void navigate(`/workflows/${w.id}`, { replace: true })
          toast.success('Workflow created')
        },
        onError: failed,
      })

      return
    }

    update.mutate(
      { ...sent, version: version ?? workflow.version },
      {
        onSuccess: (w) => {
          done(w)
          toast.success('Saved')
        },
        onError: failed,
      },
    )
  }

  // Cmd/Ctrl+S saves, wherever the focus is.
  const onSaveKey = useEffectEvent((e: KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
      e.preventDefault()
      save()
    }
  })
  useEffect(() => {
    window.addEventListener('keydown', onSaveKey)
    return () => window.removeEventListener('keydown', onSaveKey)
  }, [])

  // Closing or reloading the tab with unsaved changes asks first.
  useEffect(() => {
    if (!dirty) return

    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', warn)

    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])

  // So does leaving for another page of the app. Signing out is not held up: the
  // session is gone either way.
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty &&
      !leaving.current &&
      currentLocation.pathname !== nextLocation.pathname &&
      nextLocation.pathname !== '/login',
  )

  function updateSpec(id: string, update: (spec: NodeSpec) => NodeSpec) {
    setNodes((ns) =>
      ns.map((n) => (n.id === id ? { ...n, data: { ...n.data, spec: update(n.data.spec) } } : n)),
    )
  }

  function changeNode(id: string, patch: Partial<Pick<NodeSpec, 'name' | 'continueOnFail'>>) {
    updateSpec(id, (spec) => ({ ...spec, ...patch }))

    // The error port goes away with the setting, and so do its connections.
    if (patch.continueOnFail === false) {
      setEdges((es) => es.filter((e) => !(e.source === id && e.sourceHandle === errorPort)))
    }
  }

  function changeParam(id: string, param: string, value: unknown) {
    updateSpec(id, (spec) => ({
      ...spec,
      params: Object.fromEntries(
        Object.entries({ ...spec.params, [param]: value }).filter(([, v]) => v !== undefined),
      ),
    }))
    setProblems((ps) => ps.filter((p) => !(p.nodeId === id && p.param === param)))
  }

  function handleNodesChange(changes: NodeChange<FlowNode>[]) {
    onNodesChange(changes)

    const removed = changes.flatMap((c) => (c.type === 'remove' ? [c.id] : []))
    if (removed.length > 0) {
      setProblems((ps) => ps.filter((p) => !p.nodeId || !removed.includes(p.nodeId)))
    }
  }

  function addNode(type: NodeType, at?: XYPosition) {
    let position = at

    if (!position) {
      // A click places the node in the middle of the view, nudged off any node already there.
      const rect = canvas.current?.getBoundingClientRect()
      const centre = rect
        ? screenToFlowPosition({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 })
        : { x: 0, y: 0 }

      let p = { x: centre.x - nodeOffset.x, y: centre.y - nodeOffset.y }
      while (
        nodes.some((n) => Math.abs(n.position.x - p.x) < 10 && Math.abs(n.position.y - p.y) < 10)
      ) {
        p = { x: p.x + 30, y: p.y + 30 }
      }

      position = p
    }

    const placed = position
    setNodes((ns) => [
      ...ns.map((n) => (n.selected ? { ...n, selected: false } : n)),
      newNode(type, placed, ns),
    ])
  }

  const isValidConnection: IsValidConnection<FlowEdge> = (link) =>
    linkProblem(link, edges) === undefined

  const onConnect: OnConnect = (link) => {
    const problem = linkProblem(link, edges)
    if (problem) {
      toast.error(problem)
      return
    }

    setEdges((es) => [
      ...es,
      edgeOf({
        from: link.source,
        fromPort: link.sourceHandle ?? defaultPort,
        to: link.target,
        toPort: link.targetHandle ?? defaultPort,
      }),
    ])
  }

  return (
    <ProblemsContext value={problems}>
      <div className="flex h-full flex-col">
        <Header
          workflow={workflow}
          name={name}
          onName={setName}
          version={version}
          dirty={dirty}
          saving={saving}
          onSave={save}
          onDelete={() => setConfirmDelete(true)}
        />

        {newer && (
          <div className="flex items-center gap-3 border-b bg-amber-50 px-4 py-2 text-sm">
            A newer version was saved elsewhere. Saving here will be refused until you reload.
            <Button variant="outline" size="sm" onClick={onReload}>
              Reload
            </Button>
          </div>
        )}

        <div className="flex min-h-0 flex-1">
          <aside className="w-60 shrink-0 overflow-y-auto border-r">
            <Palette types={types} onAdd={(t) => addNode(t)} />
          </aside>

          <div
            ref={canvas}
            className="min-w-0 flex-1"
            onDragOver={(e) => {
              if (e.dataTransfer.types.includes(nodeTypeMime)) {
                e.preventDefault()
                e.dataTransfer.dropEffect = 'move'
              }
            }}
            onDrop={(e) => {
              const type = types.find((t) => t.name === e.dataTransfer.getData(nodeTypeMime))
              if (!type) return

              e.preventDefault()
              const p = screenToFlowPosition({ x: e.clientX, y: e.clientY })
              addNode(type, { x: p.x - nodeOffset.x, y: p.y - nodeOffset.y })
            }}
          >
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              onNodesChange={handleNodesChange}
              onEdgesChange={onEdgesChange}
              onConnect={onConnect}
              isValidConnection={isValidConnection}
              deleteKeyCode={['Backspace', 'Delete']}
              fitView
              fitViewOptions={fitViewOptions}
            >
              <Background />
              <Controls fitViewOptions={fitViewOptions} showInteractive={false} />
            </ReactFlow>
          </div>

          <aside className="w-80 shrink-0 overflow-y-auto border-l" aria-label="Settings">
            {selectedNode ? (
              <NodePanel
                key={selectedNode.id}
                node={selectedNode}
                onChange={(patch) => changeNode(selectedNode.id, patch)}
                onParam={(param, value) => changeParam(selectedNode.id, param, value)}
                onDelete={() => void deleteElements({ nodes: [{ id: selectedNode.id }] })}
              />
            ) : (
              <WorkflowPanel problems={problems} />
            )}
          </aside>
        </div>

        <ConfirmDialog
          open={conflict}
          title="This workflow changed elsewhere"
          description="A newer version was saved since you opened it, perhaps in another tab. Reload to continue from that version; your changes here will be lost."
          confirm="Reload"
          cancel="Keep editing"
          onConfirm={onReload}
          onCancel={() => setConflict(false)}
        />

        <ConfirmDialog
          open={blocker.state === 'blocked'}
          title="Leave without saving?"
          description="Your changes to this workflow have not been saved."
          confirm="Leave"
          cancel="Stay"
          destructive
          onConfirm={() => blocker.proceed?.()}
          onCancel={() => blocker.reset?.()}
        />

        {workflow && (
          <DeleteDialog
            workflow={workflow}
            open={confirmDelete}
            onClose={() => setConfirmDelete(false)}
            onDeleted={() => {
              leaving.current = true
              void navigate('/workflows')
            }}
          />
        )}
      </div>
    </ProblemsContext>
  )
}

type HeaderProps = {
  workflow?: Workflow
  name: string
  onName: (name: string) => void
  version?: number
  dirty: boolean
  saving: boolean
  onSave: () => void
  onDelete: () => void
}

function Header({ workflow, name, onName, version, dirty, saving, onSave, onDelete }: HeaderProps) {
  return (
    <header className="flex items-center gap-3 border-b px-4 py-2">
      <Link
        to="/workflows"
        className="flex items-center text-sm text-muted-foreground hover:text-foreground"
      >
        <ChevronLeft className="size-4" />
        Workflows
      </Link>

      <Input
        aria-label="Workflow name"
        value={name}
        onChange={(e) => onName(e.target.value)}
        className="h-8 w-72 font-medium"
      />

      {workflow && (
        <Badge variant={workflow.active ? 'default' : 'secondary'}>
          {workflow.active ? 'Active' : 'Inactive'}
        </Badge>
      )}

      <span className="text-xs text-muted-foreground">
        {version === undefined ? 'Not saved yet' : `Version ${version}`}
        {dirty && ' · Unsaved changes'}
      </span>

      <div className="ml-auto flex items-center gap-3">
        {workflow && <ActiveSwitch workflow={workflow} dirty={dirty} />}

        <Button onClick={onSave} disabled={saving || (workflow !== undefined && !dirty)}>
          {saving ? 'Saving…' : 'Save'}
        </Button>

        {workflow && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon" aria-label="More actions">
                <EllipsisVertical />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem variant="destructive" onSelect={onDelete}>
                <Trash2 />
                Delete workflow
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
    </header>
  )
}

function ActiveSwitch({ workflow, dirty }: { workflow: Workflow; dirty: boolean }) {
  const setActive = useSetActive(workflow.id)

  return (
    <div className="flex items-center gap-2" title={dirty ? 'Save your changes first' : undefined}>
      <Label htmlFor="workflow-active" className="text-sm">
        Active
      </Label>
      <Switch
        id="workflow-active"
        checked={workflow.active}
        disabled={dirty || setActive.isPending}
        onCheckedChange={(on) =>
          setActive.mutate(on, {
            onSuccess: (w) =>
              toast.success(w.active ? 'Workflow activated' : 'Workflow deactivated'),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }
      />
    </div>
  )
}

type DeleteDialogProps = {
  workflow: Workflow
  open: boolean
  onClose: () => void
  onDeleted: () => void
}

function DeleteDialog({ workflow, open, onClose, onDeleted }: DeleteDialogProps) {
  const remove = useDeleteWorkflow(workflow.id)

  return (
    <ConfirmDialog
      open={open}
      title={`Delete “${workflow.name}”?`}
      description="The workflow stops and is deleted with all its executions. This cannot be undone."
      confirm="Delete"
      destructive
      onCancel={onClose}
      onConfirm={() =>
        remove.mutate(undefined, {
          onSuccess: () => {
            toast.success('Workflow deleted')
            onDeleted()
          },
          onError: (error) => toast.error(errorMessage(error)),
        })
      }
    />
  )
}

function starter(types: readonly NodeType[]): { nodes: FlowNode[]; edges: FlowEdge[] } {
  const manual = types.find((t) => t.name === manualTrigger)
  if (!manual) return { nodes: [], edges: [] }

  return { nodes: [{ ...newNode(manual, { x: 0, y: 0 }, []), selected: false }], edges: [] }
}
