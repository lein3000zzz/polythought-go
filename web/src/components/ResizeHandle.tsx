import { cn } from 'cn'
import { useState, type PointerEvent } from 'react'

type Props = {
  /** Where the panel is, from the handle: it grows as the handle moves away from it. */
  panel: 'left' | 'right' | 'below'
  label: string
  size: number
  min: number
  max: number
  onResize: (size: number) => void
  onReset: () => void
  onDragging?: (dragging: boolean) => void
}

const step = 16 // px an arrow key moves the handle

/**
 * WAI-ARIA's window splitter, between a panel and the canvas (D369): drag it, or focus it
 * and use the arrow keys, Home for the panel's least size and End for its most; a
 * double-click puts the panel back to its own size. It takes no room: it straddles the
 * edge, a strip wider than the line it shows.
 */
export function ResizeHandle({
  panel,
  label,
  size,
  min,
  max,
  onResize,
  onReset,
  onDragging,
}: Props) {
  const [drag, setDrag] = useState<{ from: number; size: number }>()
  const across = panel !== 'below' // the handle is a vertical line, moving across
  const grow = panel === 'left' ? 1 : -1 // along the axis, the way the panel grows
  const clamp = (n: number) => Math.round(Math.min(max, Math.max(min, n)))
  const at = (e: PointerEvent) => (across ? e.clientX : e.clientY)

  const dragging = (from?: { from: number; size: number }) => {
    setDrag(from)
    onDragging?.(from !== undefined)
    // The pointer leaves the strip as it drags: the page keeps its cursor meanwhile.
    document.body.style.cursor = from ? (across ? 'col-resize' : 'row-resize') : ''
    document.body.style.userSelect = from ? 'none' : ''
  }

  const end = () => drag && dragging(undefined)

  return (
    <div className={cn('relative shrink-0', across ? 'w-0' : 'h-0')}>
      <div
        role="separator"
        aria-label={label}
        aria-orientation={across ? 'vertical' : 'horizontal'}
        aria-valuenow={size}
        aria-valuemin={min}
        aria-valuemax={max}
        tabIndex={0}
        className={cn(
          'group absolute z-10 touch-none outline-none',
          across
            ? 'inset-y-0 -left-1 w-2 cursor-col-resize'
            : 'inset-x-0 -top-1 h-2 cursor-row-resize',
        )}
        onPointerDown={(e) => {
          if (e.button !== 0) return
          e.preventDefault()
          e.currentTarget.setPointerCapture?.(e.pointerId)
          dragging({ from: at(e), size })
        }}
        onPointerMove={(e) => drag && onResize(clamp(drag.size + grow * (at(e) - drag.from)))}
        onPointerUp={end}
        onPointerCancel={end}
        onLostPointerCapture={end}
        onDoubleClick={onReset}
        onKeyDown={(e) => {
          const moves: Record<string, number> = across
            ? { ArrowLeft: -1, ArrowRight: 1 }
            : { ArrowUp: -1, ArrowDown: 1 }

          if (e.key in moves) onResize(clamp(size + grow * moves[e.key] * step))
          else if (e.key === 'Home') onResize(min)
          else if (e.key === 'End') onResize(max)
          else return

          e.preventDefault()
        }}
      >
        <div
          className={cn(
            'pointer-events-none absolute bg-primary opacity-0 transition-opacity group-hover:opacity-60 group-focus-visible:opacity-100',
            drag && 'opacity-100 group-hover:opacity-100',
            across
              ? 'inset-y-0 left-1/2 w-0.5 -translate-x-1/2'
              : 'inset-x-0 top-1/2 h-0.5 -translate-y-1/2',
          )}
        />
      </div>
    </div>
  )
}
