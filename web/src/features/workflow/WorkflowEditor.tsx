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
import { cn } from 'cn'
import {
  ChevronLeft,
  EllipsisVertical,
  History,
  PanelLeftClose,
  PanelLeftOpen,
  Play,
  Share2,
  Trash2,
  X,
} from 'lucide-react'
import { useEffect, useEffectEvent, useMemo, useRef, useState } from 'react'
import { Link, useBlocker, useNavigate } from 'react-router'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import {
  ApiError,
  errorMessage,
  type NodeType,
  type Problem,
  type Schemas,
  type Workflow,
} from '@/api/client'
import { useCancelExecution, useExecution, useExecutions, useRunWorkflow } from '@/api/executions'
import {
  useCreateWorkflow,
  useDeleteWorkflow,
  useSetActive,
  useUpdateWorkflow,
  useWorkflowProblems,
} from '@/api/workflows'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { formatDateTime, formatDuration, formatTime } from '@/lib/format'
import { useStoredFlag } from '@/lib/storage'
import { useTheme } from '@/lib/theme'

import {
  defaultPort,
  edgeOf,
  errorPort,
  followPorts,
  linkProblem,
  newNode,
  paramPorts,
  sameDefinition,
  toDefinition,
  toFlow,
  type Definition,
  type FlowEdge,
  type FlowNode,
  type NodeSpec,
} from './graph'
import { ExecutionsPanel } from './ExecutionsPanel'
import { ExecutionStatus } from './ExecutionStatus'
import { NodePanel, WorkflowPanel } from './NodePanel'
import { nodeTypeMime, Palette } from './Palette'
import { ProblemsContext } from './problems'
import { RunOutput } from './RunOutput'
import { RunsContext } from './runs'
import { receivedShapes } from './shapes'
import { ShareDialog } from './ShareDialog'
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
  const { data: me } = useMe()
  const { screenToFlowPosition, deleteElements } = useReactFlow<FlowNode, FlowEdge>()
  const { theme } = useTheme()
  const canvas = useRef<HTMLDivElement>(null)
  const search = useRef<HTMLInputElement>(null)

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
  // Opening a workflow checks it as saving would (D60): what no longer holds since its
  // node types changed, like a custom node that lost a port, is marked as a refused save
  // marks it. Only the first answer counts, so a later look does not undo edits.
  const opened = useWorkflowProblems(workflow?.id).data
  const [openedWith, setOpenedWith] = useState<Problem[]>()
  if (opened && openedWith === undefined) {
    setOpenedWith(opened)
    if (opened.length > 0) setProblems(opened)
  }
  const stale = (openedWith?.length ?? 0) > 0 && problems.length > 0
  const [conflict, setConflict] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [sharing, setSharing] = useState(false)
  const [nodeListOpen, setNodeListOpen] = useStoredFlag('polythought.nodeList', true)
  const leaving = useRef(false) // set before navigating away on purpose

  // An execution shown on the canvas: each node's status, counts and kept items.
  const [inspectedId, setInspectedId] = useState<string>()
  const [showExecutions, setShowExecutions] = useState(false)
  const [panelTab, setPanelTab] = useState<'settings' | 'output'>('settings')
  // Following shows the newest execution, which live events keep moving.
  const [follow, setFollow] = useState(false)
  const latest = useExecutions(workflow?.id ?? '', workflow !== undefined && follow).data?.[0]
  const shownId = follow ? latest?.id : inspectedId
  const inspected = useExecution(workflow?.id ?? '', workflow ? shownId : undefined)
  const runs = useMemo(
    () => inspected.data && new Map(inspected.data.runs.map((r) => [r.nodeId, r])),
    [inspected.data],
  )
  const kept = inspected.data?.runs.some((r) => r.samples !== undefined) ?? false

  const definition = useMemo(() => toDefinition(name, nodes, edges), [name, nodes, edges])
  // What the items look like where each node receives them, for its field settings.
  const shapes = useMemo(() => receivedShapes(nodes, edges), [nodes, edges])
  const dirty = !sameDefinition(definition, saved ?? initial.definition)
  const selected = nodes.filter((n) => n.selected)
  const selectedNode = selected.length === 1 ? selected[0] : undefined
  const newer = workflow !== undefined && version !== undefined && workflow.version > version

  const create = useCreateWorkflow()
  const update = useUpdateWorkflow(workflow?.id ?? '')
  const run = useRunWorkflow(workflow?.id ?? '')
  const saving = create.isPending || update.isPending

  /** Saves the workflow and reports whether it was saved. */
  async function save(): Promise<boolean> {
    if (saving) return false

    const sent = definition

    try {
      const w = workflow
        ? await update.mutateAsync({ ...sent, version: version ?? workflow.version })
        : await create.mutateAsync(sent)

      setSaved({ ...sent, name: w.name })
      setName((current) => (current === sent.name ? w.name : current)) // the server trims it
      setVersion(w.version)
      setProblems([])

      if (!workflow) {
        leaving.current = true
        void navigate(`/workflows/${w.id}`, { replace: true })
      }

      toast.success(workflow ? 'Saved' : 'Workflow created')

      return true
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true)
      } else if (error instanceof ApiError && error.problems.length > 0) {
        setProblems(error.problems)
        toast.error('Not saved: the workflow has problems, marked in red.')
      } else {
        toast.error(errorMessage(error))
      }

      return false
    }
  }

  /** Runs the saved workflow, saving unsaved changes first, and shows the result. */
  async function runNow() {
    if (dirty && !(await save())) return

    run.mutate(undefined, {
      onSuccess: (e) => {
        setFollow(false)
        setInspectedId(e.id)
        setPanelTab('output')

        if (e.status === 'succeeded')
          toast.success(`Run succeeded in ${formatDuration(e.durationMs)}`)
        else toast.error(`Run ${e.status}${e.error ? `: ${e.error}` : ''}`)
      },
      onError: (error) => toast.error(errorMessage(error)),
    })
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

  // "/" searches the node list, opening it if need be, unless the focus is in a field.
  const onSearchKey = useEffectEvent((e: KeyboardEvent) => {
    if (e.key !== '/' || e.metaKey || e.ctrlKey || e.altKey) return
    if (
      e.target instanceof Element &&
      e.target.closest('input, textarea, select, [contenteditable], .nokey')
    ) {
      return
    }

    e.preventDefault()
    setNodeListOpen(true)
    // Once the list is open: a closed one is inert and cannot take the focus.
    requestAnimationFrame(() => search.current?.focus())
  })
  useEffect(() => {
    window.addEventListener('keydown', onSearchKey)
    return () => window.removeEventListener('keydown', onSearchKey)
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

  function changeNode(
    id: string,
    patch: Partial<Pick<NodeSpec, 'name' | 'continueOnFail' | 'notes'>>,
  ) {
    updateSpec(id, (spec) => ({ ...spec, ...patch }))

    // The error port goes away with the setting, and so do its connections.
    if (patch.continueOnFail === false) {
      setEdges((es) => es.filter((e) => !(e.source === id && e.sourceHandle === errorPort)))
    }
  }

  function changeParam(id: string, param: string, value: unknown) {
    const paramsWith = (spec: NodeSpec) =>
      Object.fromEntries(
        Object.entries({ ...spec.params, [param]: value }).filter(([, v]) => v !== undefined),
      )

    updateSpec(id, (spec) => ({ ...spec, params: paramsWith(spec) }))
    setProblems((ps) => ps.filter((p) => !(p.nodeId === id && p.param === param)))

    // Connections follow the ports a setting gives, such as one per button.
    const node = nodes.find((n) => n.id === id)
    if (node?.data.type?.outputsParam === param) {
      const before = paramPorts(node.data)
      const after = paramPorts({
        ...node.data,
        spec: { ...node.data.spec, params: paramsWith(node.data.spec) },
      })
      setEdges((es) => followPorts(es, id, before, after))
    }
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
      <RunsContext value={runs}>
        <div className="flex h-full flex-col">
          <Header
            workflow={workflow}
            name={name}
            onName={setName}
            version={version}
            dirty={dirty}
            saving={saving}
            onSave={() => void save()}
            running={run.isPending}
            onRun={() => void runNow()}
            showExecutions={showExecutions}
            onToggleExecutions={() => setShowExecutions((s) => !s)}
            onShare={() => setSharing(true)}
            onDelete={() => setConfirmDelete(true)}
          />

          {workflow && inspected.data && (
            <InspectBar
              workflowId={workflow.id}
              execution={inspected.data}
              version={version}
              onClose={() => {
                setFollow(false)
                setInspectedId(undefined)
                setPanelTab('settings')
              }}
            />
          )}

          {stale && !newer && (
            <div
              role="status"
              className="border-b bg-amber-50 px-4 py-2 text-sm dark:bg-amber-950/40"
            >
              This workflow no longer matches its node types, such as a custom node that changed.
              What no longer holds is marked in red; fix it and save.
            </div>
          )}

          {newer && (
            <div className="flex items-center gap-3 border-b bg-amber-50 px-4 py-2 text-sm dark:bg-amber-950/40">
              A newer version was saved elsewhere. Saving here will be refused until you reload.
              <Button variant="outline" size="sm" onClick={onReload}>
                Reload
              </Button>
            </div>
          )}

          <div className="flex min-h-0 flex-1">
            {/* The list keeps its width inside a panel that narrows to a rail: it slides
                under the edge rather than re-wrapping, and fades as it goes. */}
            <aside
              aria-label="Node list"
              className={cn(
                'relative shrink-0 overflow-hidden border-r transition-[width] duration-200 ease-out motion-reduce:transition-none',
                nodeListOpen ? 'w-60' : 'w-11',
              )}
            >
              <div
                inert={!nodeListOpen}
                aria-hidden={!nodeListOpen}
                className={cn(
                  'flex h-full w-60 flex-col transition-opacity duration-150 motion-reduce:transition-none',
                  nodeListOpen ? 'opacity-100 delay-75' : 'opacity-0',
                )}
              >
                <Palette
                  types={types}
                  onAdd={(t) => addNode(t)}
                  searchRef={search}
                  codeOff={me?.mayWriteCode === false}
                />
              </div>

              {/* Pinned to the panel's edge, so it travels with it. */}
              <div className="absolute top-1.5 right-1.5">
                <PanelToggle open={nodeListOpen} onToggle={() => setNodeListOpen(!nodeListOpen)} />
              </div>
            </aside>

            <div className="flex min-w-0 flex-1 flex-col">
              <div
                ref={canvas}
                className="min-h-0 flex-1"
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
                  colorMode={theme}
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

              {workflow && showExecutions && (
                <ExecutionsPanel
                  workflowId={workflow.id}
                  selected={shownId}
                  onSelect={(id) => {
                    setFollow(false)
                    setInspectedId(id)
                    setPanelTab('output')
                  }}
                  follow={follow}
                  onFollow={setFollow}
                  onClose={() => setShowExecutions(false)}
                />
              )}
            </div>

            <aside className="w-80 shrink-0 overflow-y-auto border-l" aria-label="Settings">
              {selectedNode && runs && (
                <div className="flex gap-1 border-b p-2" role="tablist">
                  {(['settings', 'output'] as const).map((tab) => (
                    <Button
                      key={tab}
                      role="tab"
                      aria-selected={panelTab === tab}
                      variant={panelTab === tab ? 'secondary' : 'ghost'}
                      size="sm"
                      onClick={() => setPanelTab(tab)}
                    >
                      {tab === 'settings' ? 'Settings' : 'Output'}
                    </Button>
                  ))}
                </div>
              )}

              {selectedNode && runs && panelTab === 'output' ? (
                <RunOutput key={selectedNode.id} run={runs.get(selectedNode.id)} kept={kept} />
              ) : selectedNode ? (
                <NodePanel
                  key={selectedNode.id}
                  node={selectedNode}
                  received={shapes.get(selectedNode.id)}
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
            <ShareDialog
              workflow={workflow}
              dirty={dirty}
              open={sharing}
              onClose={() => setSharing(false)}
            />
          )}

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
      </RunsContext>
    </ProblemsContext>
  )
}

/** Opens or closes the node list on the left; the canvas takes the room. */
function PanelToggle({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  const label = open ? 'Hide node list' : 'Show node list'

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={label}
          aria-expanded={open}
          onClick={onToggle}
        >
          {open ? <PanelLeftClose /> : <PanelLeftOpen />}
        </Button>
      </TooltipTrigger>
      <TooltipContent side="right">{label}</TooltipContent>
    </Tooltip>
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
  running: boolean
  onRun: () => void
  showExecutions: boolean
  onToggleExecutions: () => void
  onShare: () => void
  onDelete: () => void
}

function Header({
  workflow,
  name,
  onName,
  version,
  dirty,
  saving,
  onSave,
  running,
  onRun,
  showExecutions,
  onToggleExecutions,
  onShare,
  onDelete,
}: HeaderProps) {
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
        {workflow && (
          <Button
            variant={showExecutions ? 'secondary' : 'ghost'}
            aria-pressed={showExecutions}
            onClick={onToggleExecutions}
          >
            <History />
            Executions
          </Button>
        )}

        {workflow && <ActiveSwitch workflow={workflow} dirty={dirty} />}

        {workflow && (
          <Button variant="outline" onClick={onRun} disabled={running || saving}>
            <Play />
            {running ? 'Running…' : dirty ? 'Save and run' : 'Run'}
          </Button>
        )}

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
              <DropdownMenuItem onSelect={onShare}>
                <Share2 />
                Share to library…
              </DropdownMenuItem>
              <DropdownMenuSeparator />
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

type InspectBarProps = {
  workflowId: string
  execution: Schemas['Execution']
  version?: number // of the workflow being edited
  onClose: () => void
}

function InspectBar({ workflowId, execution: e, version, onClose }: InspectBarProps) {
  const cancel = useCancelExecution(workflowId)

  return (
    <div className="flex items-center gap-3 border-b bg-muted/50 px-4 py-1.5 text-sm">
      <ExecutionStatus status={e.status} />
      <span>
        Execution at {formatTime(e.startedAt)}
        {e.finishedAt && ` · ${formatDuration(e.durationMs)}`} · started by {e.triggeredBy}
      </span>
      {e.status === 'waiting' && (
        <>
          <span className="text-muted-foreground">
            Waiting at {e.waitingOn}
            {e.waitingUntil && ` until ${formatDateTime(e.waitingUntil)}`}
          </span>
          <Button
            variant="outline"
            size="sm"
            disabled={cancel.isPending}
            onClick={() =>
              cancel.mutate(e.id, {
                onSuccess: () => toast.success('Execution cancelled'),
                onError: (error) => toast.error(errorMessage(error)),
              })
            }
          >
            Cancel execution
          </Button>
        </>
      )}
      {version !== undefined && e.workflowVersion !== version && (
        <span className="text-muted-foreground">
          Ran version {e.workflowVersion}; the nodes may have changed since.
        </span>
      )}
      {e.error && <span className="min-w-0 truncate text-destructive">{e.error}</span>}
      <Button variant="ghost" size="sm" className="ml-auto" onClick={onClose}>
        <X />
        Close
      </Button>
    </div>
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
