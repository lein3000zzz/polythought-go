import '@xyflow/react/dist/style.css'

import {
  Background,
  Controls,
  ReactFlow,
  ReactFlowProvider,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type EdgeChange,
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
  Keyboard,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Play,
  Redo2,
  Share2,
  Trash2,
  Undo2,
  X,
} from 'lucide-react'
import { useEffect, useEffectEvent, useMemo, useRef, useState } from 'react'
import { Link, useBlocker, useNavigate, useSearchParams } from 'react-router'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { useRunWorkflowCode } from '@/api/code'
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
import { useTradingSetup } from '@/api/trading'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ResizeHandle } from '@/components/ResizeHandle'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { TradingView } from '@/features/trading/TradingView'
import { CodeView, type ApplyFailure } from '@/features/workflowcode/CodeView'
import { place } from '@/features/workflowcode/place'
import { printWorkflow } from '@/features/workflowcode/print'
import { formatDateTime, formatDuration, formatTime } from '@/lib/format'
import { useElementSize } from '@/lib/elementSize'
import { useStoredFlag, useStoredNumber } from '@/lib/storage'
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
import { clipText, copyOf, fromClip, placed, type Copied } from './clipboard'
import { UndoHistory } from './history'
import { actionOf, inField, keymapOf, shown, type Keymap } from './shortcuts'
import { ShortcutsDialog } from './ShortcutsDialog'
import { ExecutionStatus } from './ExecutionStatus'
import { NodePanel, WorkflowPanel } from './NodePanel'
import { nodeTypeMime, Palette } from './Palette'
import { canvasMin, clamp, fitWidths, panels, rail } from './panels'
import { ProblemsContext } from './problems'
import { RunOutput } from './RunOutput'
import { localAgentType, TaskStatus } from './TaskStatus'
import { RunsContext } from './runs'
import { receivedPorts } from './shapes'
import { ShareDialog } from './ShareDialog'
import { WorkflowNodeView } from './WorkflowNodeView'

// Defined once: React Flow re-mounts every node when this object changes identity.
const nodeTypes = { workflow: WorkflowNodeView }

// The editor's views: the canvas, the workflow as code (D76), and how its trading went
// (D104), for a saved workflow with a trading node.
type View = 'canvas' | 'code' | 'trading'

const views: View[] = ['canvas', 'code', 'trading']
const viewNames: Record<View, string> = { canvas: 'Canvas', code: 'Code', trading: 'Trading' }

// A new workflow starts with this trigger, the one "Run" uses.
const manualTrigger = 'trigger.manual'

// Fitting never zooms in past 100%, so a small workflow is not blown up.
const fitViewOptions = { padding: 0.2, maxZoom: 1 }

// New nodes are placed by their top-left corner; these centre them on the target point.
const nodeOffset = { x: 120, y: 40 }

// What undo goes back to (D420): the canvas as it was, its workflow's name and description.
type Snapshot = { nodes: FlowNode[]; edges: FlowEdge[]; name: string; description: string }

// How far a nudge moves the selected nodes, as React Flow moves a focused one.
const nudge = { step: 5, far: 20 }

/** Where an arrow key moves the selected nodes, if it is one with nothing but Shift. */
function nudgeOf(e: KeyboardEvent): XYPosition | undefined {
  if (e.metaKey || e.ctrlKey || e.altKey) return undefined

  const by = e.shiftKey ? nudge.far : nudge.step
  const arrows: Record<string, XYPosition> = {
    ArrowUp: { x: 0, y: -by },
    ArrowDown: { x: 0, y: by },
    ArrowLeft: { x: -by, y: 0 },
    ArrowRight: { x: by, y: 0 },
  }

  return arrows[e.key]
}

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
  const { screenToFlowPosition, deleteElements, fitView, zoomIn, zoomOut } = useReactFlow<
    FlowNode,
    FlowEdge
  >()
  const { theme } = useTheme()
  const canvas = useRef<HTMLDivElement>(null)
  const search = useRef<HTMLInputElement>(null)

  const [initial] = useState(() => {
    const name = workflow?.name ?? 'Untitled workflow'
    const flow = workflow ? toFlow(workflow, types) : starter(types)

    return {
      ...flow,
      definition: toDefinition(name, flow.nodes, flow.edges, workflow?.description),
    }
  })

  const [name, setName] = useState(initial.definition.name)
  const [description, setDescription] = useState(initial.definition.description ?? '')
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
  const [settingsOpen, setSettingsOpen] = useStoredFlag('polythought.settingsPanel', true)
  const leaving = useRef(false) // set before navigating away on purpose

  // Undo and redo (D420): a step for each change, a save clearing them.
  const history = useRef(new UndoHistory<Snapshot>())
  const [, setSteps] = useState(0) // bumped as the history changes, for its buttons
  const dragging = useRef(false)
  // A deletion's nodes and connections go as one step: React Flow says them apart.
  const deleting = useRef(0)
  const [listingShortcuts, setListingShortcuts] = useState(false)
  const keymap: Keymap = useMemo(() => keymapOf(me?.shortcuts), [me?.shortcuts])

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

  // The panels' sizes (D369, D390), each in its bounds; the side ones give way so the
  // canvas keeps canvasMin, and get their own back as the window widens.
  const [listWidth, setListWidth] = useStoredNumber(
    'polythought.nodeListWidth',
    panels.list.initial,
  )
  const [settingsWidth, setSettingsWidth] = useStoredNumber(
    'polythought.settingsWidth',
    panels.settings.initial,
  )
  const [executionsHeight, setExecutionsHeight] = useStoredNumber(
    'polythought.executionsHeight',
    panels.executions.initial,
  )
  const [resizing, setResizing] = useState(false) // panels do not slide while dragged
  const [row, setRow] = useState<HTMLDivElement | null>(null)
  const [column, setColumn] = useState<HTMLDivElement | null>(null)
  const rowWidth = useElementSize(row).width
  const columnHeight = useElementSize(column).height
  const list = clamp(listWidth, panels.list.min, panels.list.max)
  const settings = clamp(settingsWidth, panels.settings.min, panels.settings.max)
  const [listShown, settingsShown] = fitWidths(
    rowWidth,
    nodeListOpen ? { width: list, min: panels.list.min } : { width: rail, min: rail },
    settingsOpen ? { width: settings, min: panels.settings.min } : { width: rail, min: rail },
  )
  // What a handle may drag its panel to: its bound, or what leaves the canvas its least.
  const room = (other: number) => (rowWidth > 0 ? rowWidth - canvasMin - other : Infinity)
  const listMax = clamp(
    Math.min(panels.list.max, room(settingsShown)),
    panels.list.min,
    panels.list.max,
  )
  const settingsMax = clamp(
    Math.min(panels.settings.max, room(listShown)),
    panels.settings.min,
    panels.settings.max,
  )
  const executionsMax = Math.max(
    panels.executions.min,
    columnHeight > 0 ? Math.floor(columnHeight / 2) : 2 * panels.executions.initial,
  )
  const executionsShown = clamp(executionsHeight, panels.executions.min, executionsMax)

  // The workflow as code (D76), printed when the Code view opens. base is the code the
  // canvas has: text that differs is not applied yet (D77). A link may open its Trading
  // view instead, as the dashboard's do (D429).
  const [query] = useSearchParams()
  const [view, setView] = useState<View>(query.get('view') === 'trading' ? 'trading' : 'canvas')

  // Double-clicking a node opens the settings panel on it (D390). React Flow would zoom in
  // as well: the event stops at the canvas's edge on its way in, so it never reaches the
  // zoom, which a double-click on the empty canvas still does.
  useEffect(() => {
    const el = canvas.current
    if (!el) return

    const open = (e: MouseEvent) => {
      if (!(e.target instanceof Element) || !e.target.closest('.react-flow__node')) return
      e.stopPropagation()
      setSettingsOpen(true)
    }

    el.addEventListener('dblclick', open, true)
    return () => el.removeEventListener('dblclick', open, true)
  }, [view, setSettingsOpen])
  const [code, setCode] = useState({ text: '', base: '' })
  const [applyFailure, setApplyFailure] = useState<ApplyFailure>()
  const [leaveTo, setLeaveTo] = useState<View>() // asked on leaving the Code view
  const runCode = useRunWorkflowCode()
  const unapplied = view === 'code' && code.text !== code.base
  const trading =
    workflow !== undefined && nodes.some((n) => n.data.spec.type.startsWith('trading.'))
  const codeOff = me?.mayWriteCode === false

  const definition = useMemo(
    () => toDefinition(name, nodes, edges, description),
    [name, nodes, edges, description],
  )
  // What the items look like where each node receives them, for its field settings.
  const shapes = useMemo(() => receivedPorts(nodes, edges), [nodes, edges])
  const dirty = !sameDefinition(definition, saved ?? initial.definition)
  const selected = nodes.filter((n) => n.selected)
  const selectedNode = selected.length === 1 ? selected[0] : undefined
  const newer = workflow !== undefined && version !== undefined && workflow.version > version

  const create = useCreateWorkflow()
  const update = useUpdateWorkflow(workflow?.id ?? '')
  const run = useRunWorkflow(workflow?.id ?? '')
  const saving = create.isPending || update.isPending

  /**
   * Saves the workflow and reports whether it was saved. Code not applied yet is applied
   * first (D77). Staying, a new workflow does not move to its own page.
   */
  async function save({ stay = false } = {}): Promise<boolean> {
    if (saving || runCode.isPending) return false

    let sent = definition
    if (unapplied) {
      const applied = await apply()
      if (!applied) return false

      if (workflow && sameDefinition(applied, saved ?? initial.definition)) return true
      sent = applied
    }

    try {
      const w = workflow
        ? await update.mutateAsync({ ...sent, version: version ?? workflow.version })
        : await create.mutateAsync(sent)

      setSaved({ ...sent, name: w.name, description: w.description })
      setName((current) => (current === sent.name ? w.name : current)) // the server trims it
      setDescription((current) => (current === sent.description ? w.description : current))
      setVersion(w.version)
      setProblems([])
      history.current.clear()
      setSteps((n) => n + 1)

      if (!workflow && !stay) {
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
        setView('canvas') // where they are marked; the code is applied
        toast.error('Not saved: the workflow has problems, marked in red.')
      } else {
        toast.error(errorMessage(error))
      }

      return false
    }
  }

  /**
   * Runs the code on the server and puts the workflow it builds on the canvas, as unsaved
   * changes (D66): nodes the canvas has keep their place. Gives the new definition, or
   * nothing when the code was refused, saying why under it.
   */
  async function apply(): Promise<Definition | undefined> {
    const text = code.text

    try {
      const built = place(
        await runCode.mutateAsync(text),
        new Map(nodes.map((n) => [n.id, n.position])),
      )
      const flow = toFlow(built, types)

      record()
      setNodes(flow.nodes)
      setEdges(flow.edges)
      setName(built.name)
      setProblems([])
      setApplyFailure(undefined)
      setCode((c) => ({ ...c, base: text }))

      return toDefinition(built.name, flow.nodes, flow.edges, description) // code carries none
    } catch (error) {
      setApplyFailure({
        message: error instanceof ApiError ? error.message : errorMessage(error),
        problems:
          error instanceof ApiError
            ? error.problems.flatMap((p) =>
                p.line ? [{ line: p.line, column: p.column ?? 1, message: p.message }] : [],
              )
            : [],
      })
      toast.error('Not applied: the code has a problem, shown under it.')

      return undefined
    }
  }

  function showCode() {
    const text = printWorkflow(definition, types)
    setCode({ text, base: text })
    setApplyFailure(undefined)
    setView('code')
  }

  function leave(to: View) {
    if (unapplied) setLeaveTo(to)
    else setView(to)
  }

  async function applyAndGo() {
    const to = leaveTo ?? 'canvas'
    setLeaveTo(undefined)
    if (await apply()) setView(to)
  }

  // Leaving the page with code not applied: it is kept only once saved (D78).
  async function applyAndLeave() {
    const to = blocker.location
    blocker.reset?.()

    if (to && (await save({ stay: true }))) {
      leaving.current = true
      void navigate(to)
    }
  }

  /** Runs the saved workflow, saving unsaved changes first, and shows the result. */
  async function runNow() {
    if ((dirty || unapplied) && !(await save())) return

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

  // The editor's shortcuts (D420), as the user has them. Saving goes wherever the focus is;
  // the rest are the canvas's, but in a field, where keys are the field's, or a dialog.
  const onKey = useEffectEvent((e: KeyboardEvent) => {
    const action = actionOf(e, keymap)

    if (action === 'save') {
      e.preventDefault()
      void save()
      return
    }

    if (view !== 'canvas' || e.defaultPrevented || inField(e.target)) return

    if (!action) {
      // The arrows nudge the selected nodes, as React Flow does a focused one.
      const at = nudgeOf(e)
      if (at && nodes.some((n) => n.selected)) {
        e.preventDefault()
        record('nudge')
        setNodes((ns) =>
          ns.map((n) =>
            n.selected ? { ...n, position: { x: n.position.x + at.x, y: n.position.y + at.y } } : n,
          ),
        )
      }

      return
    }

    e.preventDefault()

    switch (action) {
      case 'undo':
        return undo()
      case 'redo':
        return redo()
      case 'delete':
        return deleteSelected()
      case 'duplicate':
        return duplicate()
      case 'selectAll':
        setNodes((ns) => ns.map((n) => (n.selected ? n : { ...n, selected: true })))
        return
      case 'deselect':
        setNodes((ns) => ns.map((n) => (n.selected ? { ...n, selected: false } : n)))
        setEdges((es) => es.map((ed) => (ed.selected ? { ...ed, selected: false } : ed)))
        return
      case 'fitView':
        void fitView(fitViewOptions)
        return
      case 'zoomIn':
        void zoomIn()
        return
      case 'zoomOut':
        void zoomOut()
        return
      case 'run':
        if (workflow && !run.isPending && !saving) void runNow()
        return
      case 'search':
        setNodeListOpen(true)
        // Once the list is open: a closed one is inert and cannot take the focus.
        requestAnimationFrame(() => search.current?.focus())
        return
      case 'help':
        setListingShortcuts(true)
        return
    }
  })
  useEffect(() => {
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Copy, cut and paste are the browser's own (D420): its events carry the system
  // clipboard, which needs no permission, the selected nodes going as text.
  const onClip = useEffectEvent((e: ClipboardEvent) => {
    if (view !== 'canvas' || inField(e.target) || !e.clipboardData) return

    if (e.type === 'paste') {
      const c = fromClip(e.clipboardData.getData('text/plain'))
      if (!c) return

      e.preventDefault()
      put(c)

      return
    }

    // Text selected on the page is the browser's to copy.
    if (!nodes.some((n) => n.selected) || window.getSelection()?.toString()) return

    e.preventDefault()
    e.clipboardData.setData('text/plain', clipText(copyOf(nodes, edges)))
    pastes.current = 0

    if (e.type === 'cut') deleteSelected()
  })
  useEffect(() => {
    for (const t of ['copy', 'cut', 'paste']) document.addEventListener(t, onClip as EventListener)
    return () => {
      for (const t of ['copy', 'cut', 'paste'])
        document.removeEventListener(t, onClip as EventListener)
    }
  }, [])

  // Closing or reloading the tab with unsaved changes, or code not applied, asks first.
  const unsaved = dirty || unapplied
  useEffect(() => {
    if (!unsaved) return

    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', warn)

    return () => window.removeEventListener('beforeunload', warn)
  }, [unsaved])

  // So does leaving for another page of the app. Signing out is not held up: the
  // session is gone either way.
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      unsaved &&
      !leaving.current &&
      currentLocation.pathname !== nextLocation.pathname &&
      nextLocation.pathname !== '/login',
  )

  /** Keeps the canvas as it is now, before a change, to undo to; key joins a change to the last. */
  function record(key?: string) {
    history.current.record({ nodes, edges, name, description }, key)
    setSteps((n) => n + 1)
  }

  function restore(to: Snapshot) {
    setNodes(to.nodes.map((n) => ({ ...n, selected: false, dragging: false })))
    setEdges(to.edges.map((e) => ({ ...e, selected: false })))
    setName(to.name)
    setDescription(to.description)
    setSteps((n) => n + 1)
  }

  function undo() {
    const to = history.current.undo({ nodes, edges, name, description })
    if (to) restore(to)
  }

  function redo() {
    const to = history.current.redo({ nodes, edges, name, description })
    if (to) restore(to)
  }

  function updateSpec(id: string, update: (spec: NodeSpec) => NodeSpec) {
    setNodes((ns) =>
      ns.map((n) => (n.id === id ? { ...n, data: { ...n.data, spec: update(n.data.spec) } } : n)),
    )
  }

  function changeNode(
    id: string,
    patch: Partial<Pick<NodeSpec, 'name' | 'continueOnFail' | 'notes'>>,
  ) {
    record(`node:${id}:${Object.keys(patch).join(',')}`)
    updateSpec(id, (spec) => ({ ...spec, ...patch }))

    // The error port goes away with the setting, and so do its connections.
    if (patch.continueOnFail === false) {
      setEdges((es) => es.filter((e) => !(e.source === id && e.sourceHandle === errorPort)))
    }
  }

  function changeParam(id: string, param: string, value: unknown) {
    record(`param:${id}:${param}`)

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
    // A step before nodes go, before a drag moves them, and before a nudge, nudging on
    // joining it; selecting and measuring are no change.
    const moved = changes.filter((c) => c.type === 'position')
    if (changes.some((c) => c.type === 'remove')) {
      record(`delete:${deleting.current}`)
    } else if (moved.some((c) => c.dragging)) {
      if (!dragging.current) record()
      dragging.current = true
    } else if (moved.length > 0) {
      if (!dragging.current) record('nudge')
      dragging.current = false
    }

    onNodesChange(changes)

    const removed = changes.flatMap((c) => (c.type === 'remove' ? [c.id] : []))
    if (removed.length > 0) {
      setProblems((ps) => ps.filter((p) => !p.nodeId || !removed.includes(p.nodeId)))
    }
  }

  function handleEdgesChange(changes: EdgeChange<FlowEdge>[]) {
    if (changes.some((c) => c.type === 'remove')) record(`delete:${deleting.current}`)
    onEdgesChange(changes)
  }

  /** Deletes the selected nodes and connections, as one step. */
  function deleteSelected() {
    deleting.current++
    void deleteElements({
      nodes: nodes.filter((n) => n.selected).map((n) => ({ id: n.id })),
      edges: edges.filter((e) => e.selected).map((e) => ({ id: e.id })),
    })
  }

  // Pasting again in the same place steps each copy further off the last (D420).
  const pastes = useRef(0)

  /** Puts copied nodes on the canvas, selected: beside the originals where they are here, else mid-view. */
  function put(c: Copied) {
    if (c.nodes.length === 0) return

    let offset = { x: 40 * (pastes.current + 1), y: 40 * (pastes.current + 1) }

    const here = c.nodes.some((n) =>
      nodes.some((o) => o.id === n.id && o.data.spec.type === n.type),
    )
    if (!here) {
      const rect = canvas.current?.getBoundingClientRect()
      const centre = rect
        ? screenToFlowPosition({ x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 })
        : { x: 0, y: 0 }
      const left = Math.min(...c.nodes.map((n) => n.position?.x ?? 0))
      const top = Math.min(...c.nodes.map((n) => n.position?.y ?? 0))

      offset = { x: centre.x - nodeOffset.x - left, y: centre.y - nodeOffset.y - top }
    } else {
      pastes.current++
    }

    const got = placed(c, nodes, types, offset)
    if (got.skipped.length > 0) {
      toast.error(`Left out nodes of types you do not have: ${got.skipped.join(', ')}`)
    }

    if (got.nodes.length === 0) return

    record()
    setNodes((ns) => [...ns.map((n) => (n.selected ? { ...n, selected: false } : n)), ...got.nodes])
    setEdges((es) => [...es.map((e) => (e.selected ? { ...e, selected: false } : e)), ...got.edges])
  }

  /** Duplicates the selected nodes in place, every setting with them: they never leave the page. */
  function duplicate() {
    const c = copyOf(nodes, edges, true)
    pastes.current = 0
    put(c)
  }

  function addNode(type: NodeType, at?: XYPosition) {
    record()

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

    record()
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
            onName={(n) => {
              record('name')
              setName(n)
            }}
            version={version}
            dirty={unsaved}
            saving={saving || runCode.isPending}
            onSave={() => void save()}
            running={run.isPending}
            onRun={() => void runNow()}
            showExecutions={showExecutions}
            onToggleExecutions={() => setShowExecutions((s) => !s)}
            onShare={() => setSharing(true)}
            onDelete={() => setConfirmDelete(true)}
            view={view}
            onView={(v) => (v === 'code' ? showCode() : leave(v))}
            trading={trading}
            keymap={keymap}
            canUndo={history.current.canUndo}
            canRedo={history.current.canRedo}
            onUndo={undo}
            onRedo={redo}
            onShortcuts={() => setListingShortcuts(true)}
          />

          {workflow && inspected.data && (
            <InspectBar
              workflowId={workflow.id}
              execution={inspected.data}
              version={version}
              onTask={
                nodes.find((n) => n.id === inspected.data.waitingOn)?.data.spec.type ===
                localAgentType
              }
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

          {view === 'trading' && workflow ? (
            <div className="min-h-0 flex-1">
              <TradingView workflowId={workflow.id} />
            </div>
          ) : view === 'code' ? (
            <div className="min-h-0 flex-1">
              <CodeView
                value={code.text}
                onChange={(text) => {
                  setCode((c) => ({ ...c, text }))
                  setApplyFailure(undefined)
                }}
                onApply={() => void apply()}
                applying={runCode.isPending}
                applied={!unapplied}
                failure={applyFailure}
                codeOff={codeOff}
              />
            </div>
          ) : (
            <div ref={setRow} className="flex min-h-0 flex-1">
              {/* The list keeps its width inside a panel that narrows to a rail: it slides
                under the edge rather than re-wrapping, and fades as it goes. */}
              <aside
                aria-label="Node list"
                className={cn('relative shrink-0 overflow-hidden border-r', !resizing && sliding)}
                style={{ width: listShown }}
              >
                <div
                  inert={!nodeListOpen}
                  aria-hidden={!nodeListOpen}
                  className={cn(
                    'flex h-full flex-col transition-opacity duration-150 motion-reduce:transition-none',
                    nodeListOpen ? 'opacity-100 delay-75' : 'opacity-0',
                  )}
                  style={{ width: nodeListOpen ? listShown : list }}
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
                  <PanelToggle
                    panel="node list"
                    open={nodeListOpen}
                    onToggle={() => setNodeListOpen(!nodeListOpen)}
                  />
                </div>
              </aside>

              {nodeListOpen && (
                <ResizeHandle
                  panel="left"
                  label="Resize the node list"
                  size={listShown}
                  min={panels.list.min}
                  max={listMax}
                  onResize={setListWidth}
                  onReset={() => setListWidth(panels.list.initial)}
                  onDragging={setResizing}
                />
              )}

              <div ref={setColumn} className="flex min-w-0 flex-1 flex-col">
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
                    onEdgesChange={handleEdgesChange}
                    onConnect={onConnect}
                    isValidConnection={isValidConnection}
                    deleteKeyCode={null}
                    fitView
                    fitViewOptions={fitViewOptions}
                  >
                    <Background />
                    <Controls fitViewOptions={fitViewOptions} showInteractive={false} />
                  </ReactFlow>
                </div>

                {workflow && showExecutions && (
                  <>
                    <ResizeHandle
                      panel="below"
                      label="Resize the executions"
                      size={executionsShown}
                      min={panels.executions.min}
                      max={executionsMax}
                      onResize={setExecutionsHeight}
                      onReset={() => setExecutionsHeight(panels.executions.initial)}
                    />
                    <div className="shrink-0" style={{ height: executionsShown }}>
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
                    </div>
                  </>
                )}
              </div>

              {settingsOpen && (
                <ResizeHandle
                  panel="right"
                  label="Resize the settings"
                  size={settingsShown}
                  min={panels.settings.min}
                  max={settingsMax}
                  onResize={setSettingsWidth}
                  onReset={() => setSettingsWidth(panels.settings.initial)}
                  onDragging={setResizing}
                />
              )}

              {/* Mirrored: the content keeps to the right edge, so it slides under it. */}
              <aside
                aria-label="Settings"
                className={cn('relative shrink-0 overflow-hidden border-l', !resizing && sliding)}
                style={{ width: settingsShown }}
              >
                <div
                  inert={!settingsOpen}
                  aria-hidden={!settingsOpen}
                  className={cn(
                    'absolute inset-y-0 right-0 flex flex-col transition-opacity duration-150 motion-reduce:transition-none',
                    settingsOpen ? 'opacity-100 delay-75' : 'opacity-0',
                  )}
                  style={{ width: settingsOpen ? settingsShown : settings }}
                >
                  <div className="flex h-10 shrink-0 items-center gap-1 border-b pr-2 pl-10">
                    {selectedNode && runs ? (
                      <div className="flex gap-1" role="tablist">
                        {(['settings', 'output'] as const).map((tab) => (
                          <Button
                            key={tab}
                            role="tab"
                            aria-selected={panelTab === tab}
                            variant={panelTab === tab ? 'secondary' : 'ghost'}
                            size="sm"
                            className="h-7"
                            onClick={() => setPanelTab(tab)}
                          >
                            {tab === 'settings' ? 'Settings' : 'Output'}
                          </Button>
                        ))}
                      </div>
                    ) : (
                      <span className="text-sm text-muted-foreground">
                        {selectedNode ? 'Settings' : 'Workflow'}
                      </span>
                    )}
                  </div>

                  <div className="min-h-0 flex-1 overflow-y-auto">
                    {selectedNode && runs && panelTab === 'output' ? (
                      <RunOutput
                        key={selectedNode.id}
                        run={runs.get(selectedNode.id)}
                        kept={kept}
                        task={
                          workflow &&
                          inspected.data &&
                          selectedNode.data.spec.type === localAgentType
                            ? { workflowId: workflow.id, executionId: inspected.data.id }
                            : undefined
                        }
                      />
                    ) : selectedNode ? (
                      <NodePanel
                        key={selectedNode.id}
                        node={selectedNode}
                        workflowId={workflow?.id}
                        received={shapes.get(selectedNode.id)?.get(defaultPort)}
                        inputs={shapes.get(selectedNode.id)}
                        onChange={(patch) => changeNode(selectedNode.id, patch)}
                        onParam={(param, value) => changeParam(selectedNode.id, param, value)}
                        onDelete={() => {
                          deleting.current++
                          void deleteElements({ nodes: [{ id: selectedNode.id }] })
                        }}
                      />
                    ) : (
                      <WorkflowPanel
                        problems={problems}
                        description={description}
                        onDescription={(d) => {
                          record('description')
                          setDescription(d)
                        }}
                      />
                    )}
                  </div>
                </div>

                <div className="absolute top-1.5 left-1.5">
                  <PanelToggle
                    panel="settings"
                    open={settingsOpen}
                    onToggle={() => setSettingsOpen(!settingsOpen)}
                  />
                </div>
              </aside>
            </div>
          )}

          <ShortcutsDialog
            open={listingShortcuts}
            onOpenChange={setListingShortcuts}
            keymap={keymap}
          />

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
            open={leaveTo !== undefined}
            title="Apply your code?"
            description="It is not on the canvas yet. Discarding it leaves the canvas as it is."
            confirm="Apply"
            alternative="Discard"
            cancel="Keep editing"
            onConfirm={() => void applyAndGo()}
            onAlternative={() => {
              setView(leaveTo ?? 'canvas')
              setLeaveTo(undefined)
            }}
            onCancel={() => setLeaveTo(undefined)}
          />

          <ConfirmDialog
            open={blocker.state === 'blocked' && unapplied}
            title="Apply your code and save?"
            description="Your code is not applied, so it is neither on the canvas nor saved. Discarding leaves without it, and without the canvas's unsaved changes."
            confirm="Apply and save"
            alternative="Discard"
            cancel="Stay"
            onConfirm={() => void applyAndLeave()}
            onAlternative={() => blocker.proceed?.()}
            onCancel={() => blocker.reset?.()}
          />

          <ConfirmDialog
            open={blocker.state === 'blocked' && !unapplied}
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
              dirty={unsaved}
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
// How a side panel's width moves as it folds or opens (D369).
const sliding = 'transition-[width] duration-200 ease-out motion-reduce:transition-none'

type PanelToggleProps = {
  panel: 'node list' | 'settings'
  open: boolean
  onToggle: () => void
}

function PanelToggle({ panel, open, onToggle }: PanelToggleProps) {
  const label = `${open ? 'Hide' : 'Show'} ${panel}`
  const left = panel === 'node list'

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
          {left ? (
            open ? (
              <PanelLeftClose />
            ) : (
              <PanelLeftOpen />
            )
          ) : open ? (
            <PanelRightClose />
          ) : (
            <PanelRightOpen />
          )}
        </Button>
      </TooltipTrigger>
      <TooltipContent side={left ? 'right' : 'left'}>{label}</TooltipContent>
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
  view: View
  onView: (view: View) => void
  trading: boolean // the workflow trades: its results have a view
  keymap: Keymap
  canUndo: boolean
  canRedo: boolean
  onUndo: () => void
  onRedo: () => void
  onShortcuts: () => void
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
  view,
  onView,
  trading,
  keymap,
  canUndo,
  canRedo,
  onUndo,
  onRedo,
  onShortcuts,
}: HeaderProps) {
  const keys = (action: keyof Keymap) => shown(keymap[action][0])

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
        {view === 'canvas' && (
          <div className="flex items-center">
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="Undo"
                  disabled={!canUndo}
                  onClick={onUndo}
                >
                  <Undo2 />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Undo ({keys('undo')})</TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="Redo"
                  disabled={!canRedo}
                  onClick={onRedo}
                >
                  <Redo2 />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Redo ({keys('redo')})</TooltipContent>
            </Tooltip>
          </div>
        )}

        {/* First, so nothing moves as it comes and goes with the Canvas view. */}
        {workflow && view === 'canvas' && (
          <Button
            variant={showExecutions ? 'secondary' : 'ghost'}
            aria-pressed={showExecutions}
            onClick={onToggleExecutions}
          >
            <History />
            Executions
          </Button>
        )}

        <div role="tablist" aria-label="View" className="flex rounded-md border p-0.5">
          {views
            .filter((v) => v !== 'trading' || trading)
            .map((v) => (
              <Button
                key={v}
                role="tab"
                aria-selected={view === v}
                variant={view === v ? 'secondary' : 'ghost'}
                size="sm"
                className="h-7"
                onClick={() => view !== v && onView(v)}
              >
                {viewNames[v]}
              </Button>
            ))}
        </div>

        {workflow && <ActiveSwitch workflow={workflow} dirty={dirty} />}

        {workflow && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="outline" onClick={onRun} disabled={running || saving}>
                <Play />
                {running ? 'Running…' : dirty ? 'Save and run' : 'Run'}
              </Button>
            </TooltipTrigger>
            <TooltipContent>{keys('run')}</TooltipContent>
          </Tooltip>
        )}

        <Tooltip>
          <TooltipTrigger asChild>
            <Button onClick={onSave} disabled={saving || (workflow !== undefined && !dirty)}>
              {saving ? 'Saving…' : 'Save'}
            </Button>
          </TooltipTrigger>
          <TooltipContent>{keys('save')}</TooltipContent>
        </Tooltip>

        {workflow && (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon" aria-label="More actions">
                <EllipsisVertical />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={onShortcuts}>
                <Keyboard />
                Keyboard shortcuts
                <DropdownMenuShortcut>{keys('help')}</DropdownMenuShortcut>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
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
  onTask: boolean // it waits at a Local agent node (D413)
  onClose: () => void
}

function InspectBar({ workflowId, execution: e, version, onTask, onClose }: InspectBarProps) {
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
          {onTask ? (
            <TaskStatus workflowId={workflowId} executionId={e.id} />
          ) : (
            <span className="text-muted-foreground">
              Waiting at {e.waitingOn}
              {e.waitingUntil && ` until ${formatDateTime(e.waitingUntil)}`}
            </span>
          )}
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
  const setup = useTradingSetup(workflow.id, workflow.active)
  const [asking, setAsking] = useState(false)
  const [cancelOrders, setCancelOrders] = useState(false)
  const resting = setup.data?.resting ?? 0

  const change = (active: boolean, cancel = false) => {
    setAsking(false)
    setActive.mutate(
      { active, cancelOrders: cancel },
      {
        onSuccess: (w) => toast.success(w.active ? 'Workflow activated' : 'Workflow deactivated'),
        onError: (error) => toast.error(errorMessage(error)),
      },
    )
  }

  return (
    <div className="flex items-center gap-2" title={dirty ? 'Save your changes first' : undefined}>
      <Label htmlFor="workflow-active" className="text-sm">
        Active
      </Label>
      <Switch
        id="workflow-active"
        checked={workflow.active}
        disabled={dirty || setActive.isPending}
        onCheckedChange={(on) => {
          // Switched off, its live orders keep resting unless its owner says (D217).
          if (!on && resting > 0) {
            setCancelOrders(false)
            setAsking(true)
            return
          }

          change(on)
        }}
      />

      <AlertDialog open={asking} onOpenChange={(open) => !open && setAsking(false)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Switch off {workflow.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              {resting === 1 ? 'Its live order rests' : `Its ${resting} live orders rest`} on the
              book:
              {resting === 1
                ? ' it keeps resting, and may fill,'
                : ' they keep resting, and may fill,'}{' '}
              unless you cancel {resting === 1 ? 'it' : 'them'}.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <div className="flex items-center gap-2 text-sm">
            <Checkbox
              id="cancel-resting"
              checked={cancelOrders}
              onCheckedChange={(checked) => setCancelOrders(checked === true)}
            />
            <Label htmlFor="cancel-resting">
              Also cancel{' '}
              {resting === 1 ? 'its resting live order' : `its ${resting} resting live orders`}
            </Label>
          </div>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={() => change(false, cancelOrders)}>
              Switch off
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
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
