import { describe, expect, it } from 'vitest'

import { actionOf, bindingOf, bindingProblem, keymapOf, shown, takenBy } from './shortcuts'

const press = (
  key: string,
  code: string,
  mods: Partial<Record<'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey', boolean>> = {},
) => new KeyboardEvent('keydown', { key, code, ...mods })

describe('the keys a press makes (D420)', () => {
  it('names them as bindings, mod being Cmd on a Mac and Ctrl elsewhere', () => {
    expect(bindingOf(press('z', 'KeyZ', { metaKey: true }), true)).toBe('mod+z')
    expect(bindingOf(press('z', 'KeyZ', { ctrlKey: true }), false)).toBe('mod+z')
    expect(bindingOf(press('Z', 'KeyZ', { metaKey: true, shiftKey: true }), true)).toBe(
      'mod+shift+z',
    )
    expect(bindingOf(press('z', 'KeyZ', { ctrlKey: true }), true), 'Ctrl on a Mac is its own').toBe(
      'ctrl+z',
    )
  })

  it('takes a letter by where it is on another layout, a digit always so', () => {
    expect(bindingOf(press('я', 'KeyZ', { ctrlKey: true }), false)).toBe('mod+z')
    expect(bindingOf(press('!', 'Digit1', { shiftKey: true }), false)).toBe('shift+1')
  })

  it('takes a symbol as it is, its Shift with it', () => {
    expect(bindingOf(press('?', 'Slash', { shiftKey: true }), false)).toBe('?')
    expect(bindingOf(press('+', 'Equal', { shiftKey: true }), false)).toBe('plus')
    expect(bindingOf(press('Enter', 'Enter', { ctrlKey: true }), false)).toBe('mod+enter')
    expect(
      bindingOf(press('Shift', 'ShiftLeft', { shiftKey: true }), false),
      'a modifier alone',
    ).toBe('')
  })

  it('finds the action, as the user set it', () => {
    expect(actionOf(press('y', 'KeyY', { ctrlKey: true }), keymapOf(undefined), false)).toBe('redo')

    const own = keymapOf({ undo: 'mod+u' })
    expect(actionOf(press('u', 'KeyU', { ctrlKey: true }), own, false)).toBe('undo')
    expect(actionOf(press('z', 'KeyZ', { ctrlKey: true }), own, false)).toBeUndefined()
  })

  it('shows them as a keyboard has them', () => {
    expect(shown('mod+shift+z', true)).toBe('⌘⇧Z')
    expect(shown('mod+shift+z', false)).toBe('Ctrl+Shift+Z')
    expect(shown('plus', false)).toBe('+')
    expect(shown('escape', false)).toBe('Esc')
  })

  it('refuses keys no shortcut may have, and says which has them', () => {
    expect(bindingProblem('shift')).toBeDefined()
    expect(bindingProblem('mod+v')).toBe('Copy, cut and paste keep their keys')
    expect(bindingProblem('mod+w')).toBe('The browser keeps that one')
    expect(bindingProblem('arrowup')).toBeDefined()
    expect(bindingProblem('mod+k')).toBeUndefined()

    expect(takenBy('mod+z', 'redo', keymapOf(undefined))?.action).toBe('undo')
    expect(takenBy('mod+z', 'undo', keymapOf(undefined))).toBeUndefined()
  })
})
