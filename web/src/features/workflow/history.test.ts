import { describe, expect, it } from 'vitest'

import { UndoHistory } from './history'

describe('the undo history (D420)', () => {
  it('goes back and forward a step at a time', () => {
    const h = new UndoHistory<string>()
    h.record('a', undefined, 0)
    h.record('b', undefined, 10)

    expect(h.undo('c')).toBe('b')
    expect(h.undo('b')).toBe('a')
    expect(h.undo('a')).toBeUndefined()
    expect(h.redo('a')).toBe('b')
    expect(h.redo('b')).toBe('c')
    expect(h.canRedo).toBe(false)
  })

  it('joins a change to the last of the same thing soon after, as typing on', () => {
    const h = new UndoHistory<string>(100, 1000)
    h.record('a', 'name', 0)
    h.record('ab', 'name', 500)
    h.record('abc', 'name', 1400)
    h.record('abcd', 'name', 3000)

    expect(h.undo('abcde')).toBe('abcd')
    expect(h.undo('abcd'), 'the first three typed together').toBe('a')
  })

  it('forgets what was undone once something else changes, keeps so many, and clears', () => {
    const h = new UndoHistory<number>(2)
    h.record(1)
    h.record(2)
    h.record(3)

    expect(h.undo(4)).toBe(3)
    h.record(30)
    expect(h.canRedo).toBe(false)
    expect(h.undo(31)).toBe(30)
    expect(h.undo(30)).toBe(2)
    expect(h.undo(2)).toBeUndefined()

    h.record(5)
    h.clear()
    expect(h.canUndo).toBe(false)
  })
})
