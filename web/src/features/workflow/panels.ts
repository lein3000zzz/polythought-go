/** The editor's panels and their bounds, in pixels (D369, D390). */
export const panels = {
  list: { initial: 240, min: 200, max: 400 },
  settings: { initial: 320, min: 280, max: 640 },
  executions: { initial: 224, min: 160 }, // and at most half the editor's height
}

/** A folded panel's width. */
export const rail = 44

/** The least the canvas keeps beside the panels. */
export const canvasMin = 320

type Width = { width: number; min: number }

/**
 * The side panels' widths in a row this wide: when they would leave the canvas less than
 * canvasMin, the settings panel gives way first, then the node list, neither under its
 * least. A row not measured yet takes them as they are.
 */
export function fitWidths(row: number, list: Width, settings: Width): [number, number] {
  let over = list.width + settings.width - (row - canvasMin)
  if (row <= 0 || over <= 0) return [list.width, settings.width]

  const s = Math.max(settings.min, settings.width - over)
  over -= settings.width - s

  return [Math.max(list.min, list.width - over), s]
}

export function clamp(n: number, min: number, max: number): number {
  return Math.min(Math.max(n, min), Math.max(min, max))
}
