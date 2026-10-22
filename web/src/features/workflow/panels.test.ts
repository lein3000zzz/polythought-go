import { describe, expect, it } from 'vitest'

import { canvasMin, clamp, fitWidths } from './panels'

const list = { width: 300, min: 200 }
const settings = { width: 500, min: 280 }

describe('fitWidths', () => {
  it('leaves the panels as they are with room to spare, or before the row is measured', () => {
    expect(fitWidths(1600, list, settings)).toEqual([300, 500])
    expect(fitWidths(300 + 500 + canvasMin, list, settings)).toEqual([300, 500])
    expect(fitWidths(0, list, settings)).toEqual([300, 500])
  })

  it('narrows the settings panel first, then the node list, keeping the canvas its least', () => {
    expect(fitWidths(1000, list, settings)).toEqual([300, 380])
    expect(fitWidths(800, list, settings)).toEqual([200, 280])
  })

  it('narrows neither under its least, even when the canvas gets less', () => {
    expect(fitWidths(600, list, settings)).toEqual([200, 280])
  })

  it('leaves a folded panel at its rail', () => {
    expect(fitWidths(700, list, { width: 44, min: 44 })).toEqual([300, 44])
  })
})

describe('clamp', () => {
  it('keeps to the bounds, the least winning over a most below it', () => {
    expect(clamp(500, 200, 400)).toBe(400)
    expect(clamp(100, 200, 400)).toBe(200)
    expect(clamp(300, 200, 150)).toBe(200)
  })
})
