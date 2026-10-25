/**
 * The editor's undo history (D420): what the canvas was before each change, to go back to,
 * and what an undo left, to go forward to again. A change of the same thing soon after the
 * last, such as typing on in one field or nudging on, joins its step.
 */
export class UndoHistory<T> {
  private past: T[] = []
  private future: T[] = []
  private last?: { key: string; at: number }
  private readonly most: number
  private readonly joinWithin: number // ms

  constructor(most = 100, joinWithin = 1000) {
    this.most = most
    this.joinWithin = joinWithin
  }

  /** Keeps before as the step to undo to, unless it joins the last step. */
  record(before: T, key?: string, now = Date.now()): void {
    const joins =
      key !== undefined && this.last?.key === key && now - this.last.at < this.joinWithin
    this.last = key === undefined ? undefined : { key, at: now }

    if (joins) return

    this.past.push(before)
    if (this.past.length > this.most) this.past.shift()
    this.future = []
  }

  /** What to go back to, keeping current to go forward to; nothing with nothing to undo. */
  undo(current: T): T | undefined {
    const to = this.past.pop()
    if (to === undefined) return undefined

    this.future.push(current)
    this.last = undefined

    return to
  }

  /** What to go forward to, keeping current to undo to; nothing with nothing undone. */
  redo(current: T): T | undefined {
    const to = this.future.pop()
    if (to === undefined) return undefined

    this.past.push(current)
    this.last = undefined

    return to
  }

  get canUndo(): boolean {
    return this.past.length > 0
  }

  get canRedo(): boolean {
    return this.future.length > 0
  }

  clear(): void {
    this.past = []
    this.future = []
    this.last = undefined
  }
}
