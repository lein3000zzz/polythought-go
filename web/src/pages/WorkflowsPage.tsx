import { GripVertical, Pin, PinOff, Plus, Search } from 'lucide-react'
import {
  type KeyboardEvent,
  type PointerEvent,
  useDeferredValue,
  useEffect,
  useRef,
  useState,
} from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { errorMessage, type Workflow } from '@/api/client'
import {
  type WorkflowFilter,
  type WorkflowStats,
  useOrderPins,
  usePinWorkflow,
  usePinnedWorkflows,
  useWorkflowStats,
  useWorkflows,
} from '@/api/workflows'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Profit } from '@/features/trading/Profit'
import { formatAgo, formatDateTime } from '@/lib/format'

type Shown = 'all' | 'on' | 'off'

const shownLabels: Record<Shown, string> = { all: 'All', on: 'Active', off: 'Inactive' }

/**
 * Your workflows as cards, the pinned first, in the order you drag them to; a search of
 * names and descriptions, and whether they are on, narrows both.
 */
export function WorkflowsPage() {
  const [query, setQuery] = useState('')
  const [shown, setShown] = useState<Shown>('all')
  const search = useRef<HTMLInputElement>(null)

  const q = useDeferredValue(query.trim())
  const filter: WorkflowFilter = { q, active: shown === 'all' ? undefined : shown === 'on' }
  const narrowed = q !== '' || shown !== 'all'

  const pinned = usePinnedWorkflows(filter)
  const rest = useWorkflows(filter, false)
  const others = rest.data?.pages.flatMap((page) => page.workflows) ?? []
  const loaded = pinned.isSuccess && rest.isSuccess
  const none = loaded && (pinned.data?.length ?? 0) === 0 && others.length === 0

  // / jumps to the search, as in the editor, unless one is typing somewhere.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      const target = e.target as HTMLElement
      if (e.key !== '/' || target.closest('input, textarea, [contenteditable="true"]')) return

      e.preventDefault()
      search.current?.focus()
    }

    window.addEventListener('keydown', onKey)

    return () => window.removeEventListener('keydown', onKey)
  }, [])

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="font-heading text-xl font-semibold">Workflows</h1>
        <Button asChild>
          <Link to="/workflows/new">
            <Plus />
            New workflow
          </Link>
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative min-w-60 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            ref={search}
            type="search"
            aria-label="Search workflows"
            placeholder="Search names and descriptions (press /)"
            className="pl-8"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
        <div role="group" aria-label="Show" className="flex gap-1">
          {(Object.keys(shownLabels) as Shown[]).map((s) => (
            <Button
              key={s}
              size="sm"
              variant={shown === s ? 'secondary' : 'ghost'}
              aria-pressed={shown === s}
              onClick={() => setShown(s)}
            >
              {shownLabels[s]}
            </Button>
          ))}
        </div>
      </div>

      {(pinned.isPending || rest.isPending) && (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Skeleton className="h-32" />
          <Skeleton className="h-32" />
          <Skeleton className="h-32" />
        </div>
      )}

      {pinned.isError && <ErrorAlert error={pinned.error} onRetry={() => void pinned.refetch()} />}
      {rest.isError && <ErrorAlert error={rest.error} onRetry={() => void rest.refetch()} />}

      {none && !narrowed && (
        <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed p-8 text-center text-sm">
          <p className="font-medium">No workflows yet.</p>
          <p className="max-w-md text-muted-foreground">
            New here? The Library has ready-made scenarios that show what workflows do, step by
            step. Make one your own, then change it.
          </p>
          <Button asChild variant="outline">
            <Link to="/library">Browse the library</Link>
          </Button>
        </div>
      )}

      {none && narrowed && (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
          No workflows match.
        </p>
      )}

      {pinned.data && pinned.data.length > 0 && (
        <Pinned workflows={pinned.data} reorder={!narrowed} />
      )}

      {others.length > 0 && (
        <section aria-labelledby="workflows-others" className="flex flex-col gap-3">
          {pinned.data && pinned.data.length > 0 && (
            <h2 id="workflows-others" className="text-sm font-medium text-muted-foreground">
              Other workflows
            </h2>
          )}
          <ul aria-label="Workflows" className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {others.map((w) => (
              <li key={w.id}>
                <WorkflowCard workflow={w} />
              </li>
            ))}
          </ul>
        </section>
      )}

      {rest.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          onClick={() => void rest.fetchNextPage()}
          disabled={rest.isFetchingNextPage}
        >
          Load more
        </Button>
      )}
    </div>
  )
}

type PinnedProps = {
  workflows: Workflow[]
  reorder: boolean // a search or filter hides some: the order is not all there to change
}

/**
 * The pinned workflows, in their order: dragged with the pointer to another place, or moved
 * with the arrow keys on a handle.
 *
 * Dragging is pointer events, not HTML drag and drop: each card's place is measured as the
 * drag starts and stays put, the dragged card follows the pointer, and the others slide to
 * where they would go. Nothing moves in the page until the drop, so the place under the
 * pointer never changes beneath it.
 */
function Pinned({ workflows: given, reorder }: PinnedProps) {
  const order = useOrderPins()
  const items = useRef(new Map<string, HTMLLIElement>())
  const press = useRef<Press>(undefined)
  const dragged = useRef(false) // a drag just ended: the click it ends with opens nothing
  const [drag, setDrag] = useState<Drag>()
  const [said, setSaid] = useState('')

  // The order just set, shown until the update settles, so a dropped card stays put
  // while it is on its way; by then the list has it.
  const [moved, setMoved] = useState<Workflow[]>()
  const workflows = moved && sameIDs(moved, given) ? moved : given

  const commit = (next: Workflow[], w: Workflow) => {
    if (sameOrder(next, workflows)) return

    const place = next.findIndex((x) => x.id === w.id) + 1
    setSaid(`${w.name} moved to place ${place} of ${next.length}`)
    setMoved(next)
    order.mutate(next, {
      onError: (error) => toast.error(errorMessage(error)),
      onSettled: () => setMoved(undefined),
    })
  }

  const end = (drop: boolean) => {
    const d = press.current
    press.current = undefined

    if (!d?.drag) return

    setDrag(undefined)
    document.body.style.removeProperty('user-select')
    // The click that ends a drag comes before this runs; one that does not come must not
    // swallow the next.
    setTimeout(() => (dragged.current = false))

    const w = workflows.find((x) => x.id === d.id)
    if (drop && w) commit(moveTo(workflows, w, d.drag.to), w)
  }

  // Escape puts a card being dragged back where it was.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape' && press.current?.drag) end(false)
    }

    window.addEventListener('keydown', onKey)

    return () => window.removeEventListener('keydown', onKey)
  })

  const onPointerDown = (e: PointerEvent<HTMLLIElement>, w: Workflow) => {
    const target = e.target as HTMLElement
    const onHandle = target.closest('[data-handle]') !== null

    // A button of the card's own, such as its pin, is pressed, not dragged; a finger
    // scrolls the page unless it holds the handle.
    if (!reorder || e.button !== 0 || (!onHandle && target.closest('button'))) return
    if (e.pointerType === 'touch' && !onHandle) return

    press.current = { id: w.id, pointer: e.pointerId, x: e.clientX, y: e.clientY }
    ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
  }

  const onPointerMove = (e: PointerEvent<HTMLLIElement>) => {
    const d = press.current
    if (!d || e.pointerId !== d.pointer) return

    const at = { x: e.clientX + window.scrollX, y: e.clientY + window.scrollY }

    if (!d.drag) {
      if (Math.hypot(e.clientX - d.x, e.clientY - d.y) < dragFrom) return

      // Where every card is, now, in the page: the places a card can go to.
      const places = workflows.map((x) => pageBox(items.current.get(x.id)))
      const from = workflows.findIndex((x) => x.id === d.id)

      d.drag = {
        id: d.id,
        from,
        to: from,
        places,
        // Where it was pressed, not where the pointer is by now: the card does not jump.
        grab: {
          x: d.x + window.scrollX - places[from].x,
          y: d.y + window.scrollY - places[from].y,
        },
        at,
      }
      dragged.current = true
      document.body.style.setProperty('user-select', 'none')
    }

    const corner = { x: at.x - d.drag.grab.x, y: at.y - d.drag.grab.y } // the card's, as it is dragged
    const next: Drag = { ...d.drag, at, to: nearest(d.drag.places, corner) }
    d.drag = next
    setDrag(next)
  }

  // Where each card shows while one is dragged: the dragged under the pointer, the others
  // in the places the new order gives them.
  const offset = (w: Workflow, i: number): { x: number; y: number } | undefined => {
    if (!drag) return undefined

    const id = drag.id
    if (w.id === id) {
      return {
        x: drag.at.x - drag.grab.x - drag.places[i].x,
        y: drag.at.y - drag.grab.y - drag.places[i].y,
      }
    }

    const moved = workflows.find((x) => x.id === id)
    const to = moved ? moveTo(workflows, moved, drag.to).findIndex((x) => x.id === w.id) : i

    return { x: drag.places[to].x - drag.places[i].x, y: drag.places[to].y - drag.places[i].y }
  }

  const onKeyDown = (e: KeyboardEvent, w: Workflow) => {
    const by = { ArrowLeft: -1, ArrowUp: -1, ArrowRight: 1, ArrowDown: 1 }[e.key]
    if (!by) return

    e.preventDefault()

    const at = workflows.findIndex((x) => x.id === w.id)
    const to = Math.min(Math.max(at + by, 0), workflows.length - 1)
    if (to !== at) commit(moveTo(workflows, w, to), w)
  }

  return (
    <section aria-labelledby="workflows-pinned" className="flex flex-col gap-3">
      <div className="flex items-baseline justify-between gap-3">
        <h2 id="workflows-pinned" className="text-sm font-medium text-muted-foreground">
          Pinned
        </h2>
        {reorder ? (
          <p className="text-xs text-muted-foreground">Drag them into the order you want.</p>
        ) : (
          <p className="text-xs text-muted-foreground">Clear the search to reorder them.</p>
        )}
      </div>
      <ul aria-label="Pinned workflows" className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {workflows.map((w, i) => {
          const shift = offset(w, i)
          const lifted = drag?.id === w.id

          return (
            <li
              key={w.id}
              ref={(el) => {
                if (el) items.current.set(w.id, el)
                else items.current.delete(w.id)
              }}
              onPointerDown={(e) => onPointerDown(e, w)}
              onPointerMove={onPointerMove}
              onPointerUp={() => end(true)}
              onPointerCancel={() => end(false)}
              onLostPointerCapture={() => press.current?.drag && end(true)}
              onClickCapture={(e) => {
                if (!dragged.current) return

                dragged.current = false
                e.preventDefault()
                e.stopPropagation()
              }}
              data-dragging={lifted || undefined}
              style={
                shift
                  ? {
                      transform: `translate(${shift.x}px, ${shift.y}px)`,
                      transition: lifted ? 'none' : 'transform 150ms ease',
                    }
                  : undefined
              }
              className={
                lifted
                  ? 'relative z-20 cursor-grabbing rounded-xl shadow-lg'
                  : reorder
                    ? 'relative cursor-grab'
                    : 'relative'
              }
            >
              <WorkflowCard
                workflow={w}
                handle={
                  reorder && (
                    <Button
                      data-handle
                      variant="ghost"
                      size="icon-sm"
                      className="cursor-grab touch-none text-muted-foreground"
                      aria-label={`Move ${w.name}`}
                      title="Drag to reorder, or use the arrow keys"
                      onKeyDown={(e) => onKeyDown(e, w)}
                    >
                      <GripVertical />
                    </Button>
                  )
                }
              />
            </li>
          )
        })}
      </ul>
      <p aria-live="polite" className="sr-only">
        {said}
      </p>
    </section>
  )
}

// dragFrom is how far, in pixels, a press moves before it drags: less is a click.
const dragFrom = 6

type Point = { x: number; y: number }

type Drag = {
  id: string // the workflow dragged
  from: number // its place as it started
  to: number // the place it would drop in
  places: Point[] // every card's top left, in the page, as the drag started
  grab: Point // where in its card it was taken
  at: Point // the pointer, in the page
}

type Press = { id: string; pointer: number; x: number; y: number; drag?: Drag }

// pageBox is an element's top left, in the page.
function pageBox(el: HTMLElement | undefined): Point {
  const r = el?.getBoundingClientRect()

  return { x: (r?.left ?? 0) + window.scrollX, y: (r?.top ?? 0) + window.scrollY }
}

// nearest is the place closest to where the dragged card's top left is, the cards being
// one size in a row.
function nearest(places: Point[], at: Point): number {
  let best = 0
  let gap = Infinity

  places.forEach((p, i) => {
    const d = Math.hypot(at.x - p.x, at.y - p.y)
    if (d < gap) [best, gap] = [i, d]
  })

  return best
}

function sameOrder(a: Workflow[], b: Workflow[]): boolean {
  return a.length === b.length && a.every((w, i) => w.id === b[i].id)
}

// sameIDs says a and b are the same workflows, in any order.
function sameIDs(a: Workflow[], b: Workflow[]): boolean {
  const ids = new Set(b.map((w) => w.id))
  return a.length === b.length && a.every((w) => ids.has(w.id))
}

// moveTo is the order with w at index to.
function moveTo(list: Workflow[], w: Workflow, to: number): Workflow[] {
  const without = list.filter((x) => x.id !== w.id)
  return [...without.slice(0, to), w, ...without.slice(to)]
}

type CardProps = {
  workflow: Workflow
  handle?: React.ReactNode // the pinned's, to move it
}

function WorkflowCard({ workflow: w, handle }: CardProps) {
  const pin = usePinWorkflow()
  const pinned = (w.pin ?? 0) > 0
  // One request for every card: they share the query.
  const stats = useWorkflowStats().data?.workflows.find((s) => s.id === w.id)

  return (
    <Card className="relative h-full transition-colors hover:bg-muted/40">
      <CardHeader>
        <CardTitle className="min-w-0 truncate">
          <Link
            to={`/workflows/${w.id}`}
            draggable={false}
            className="after:absolute after:inset-0 focus-visible:underline focus-visible:outline-none"
          >
            {w.name}
          </Link>
        </CardTitle>
        <CardAction className="relative z-10 flex items-center">
          {handle}
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={pinned ? `Unpin ${w.name}` : `Pin ${w.name}`}
            aria-pressed={pinned}
            title={pinned ? 'Unpin' : 'Pin to the top'}
            disabled={pin.isPending}
            className={pinned ? 'text-foreground' : 'text-muted-foreground'}
            onClick={() =>
              pin.mutate(
                { id: w.id, pinned: !pinned },
                { onError: (error) => toast.error(errorMessage(error)) },
              )
            }
          >
            {pinned ? <PinOff /> : <Pin />}
          </Button>
        </CardAction>
        <CardDescription className="line-clamp-3 break-words">
          {w.description || <span className="italic">No description yet.</span>}
        </CardDescription>
      </CardHeader>
      <CardFooter className="mt-auto flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <Badge variant={w.active ? 'default' : 'secondary'}>
          {w.active ? 'Active' : 'Inactive'}
        </Badge>
        {stats && <StatsLine stats={stats} />}
        <span>
          {w.nodes.length} {w.nodes.length === 1 ? 'node' : 'nodes'}
        </span>
        <span>· updated {formatDateTime(w.updatedAt)}</span>
      </CardFooter>
    </Card>
  )
}

/**
 * What a workflow made in the mode it trades in now, and its last run (D429): +$12.40
 * paper · ran 5 min ago, the run in red where it failed. Only the run where it does not
 * trade.
 */
function StatsLine({ stats: s }: { stats: WorkflowStats }) {
  const t = s.trading
  const last = s.runs.last

  // Spaces between the parts, for screen readers; the row's gap lays them out.
  return (
    <span className="flex items-center gap-1">
      {t && (
        <>
          {t.unpriced > 0 && (
            <span
              title={`${t.unpriced} open ${t.unpriced === 1 ? 'position has' : 'positions have'} no price now: at cost`}
            >
              ≈
            </span>
          )}{' '}
          <Profit text={t.profit} className="font-medium" /> <span>{t.mode}</span>{' '}
          <span aria-hidden>·</span>{' '}
        </>
      )}
      {last ? <LastRun run={last} /> : <span>never ran</span>}
    </span>
  )
}

function LastRun({ run }: { run: NonNullable<WorkflowStats['runs']['last']> }) {
  const ago = formatAgo(run.finishedAt ?? run.startedAt)

  switch (run.status) {
    case 'running':
      return <span>running now</span>
    case 'waiting':
      return <span>waiting, from {ago}</span>
    case 'cancelled':
      return <span>cancelled {ago}</span>
    case 'failed':
      return (
        <span className="flex items-center gap-1 text-destructive" title="The last run failed">
          <span aria-hidden className="size-1.5 rounded-full bg-destructive" />
          failed {ago}
        </span>
      )
    default:
      return <span>ran {ago}</span>
  }
}
