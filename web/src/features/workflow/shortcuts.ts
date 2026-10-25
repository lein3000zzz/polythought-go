/**
 * The workflow editor's shortcuts (D420): what each does, its keys to start with, and the
 * user's own where they changed them in Settings. A binding is its keys in a line, such as
 * mod+z, mod being Cmd on a Mac and Ctrl elsewhere.
 */

export type ShortcutGroup = 'Editing' | 'Selection' | 'View' | 'Workflow'

export type ShortcutAction =
  | 'undo'
  | 'redo'
  | 'duplicate'
  | 'delete'
  | 'selectAll'
  | 'deselect'
  | 'fitView'
  | 'zoomIn'
  | 'zoomOut'
  | 'save'
  | 'run'
  | 'search'
  | 'help'

export type Shortcut = {
  action: ShortcutAction
  label: string
  group: ShortcutGroup
  keys: string[] // to start with; the first is the one shown
}

/** The shortcuts a user may change, in the order the list shows them. */
export const shortcuts: Shortcut[] = [
  { action: 'undo', label: 'Undo', group: 'Editing', keys: ['mod+z'] },
  { action: 'redo', label: 'Redo', group: 'Editing', keys: ['mod+shift+z', 'mod+y'] },
  { action: 'duplicate', label: 'Duplicate the selected nodes', group: 'Editing', keys: ['mod+d'] },
  {
    action: 'delete',
    label: 'Delete the selection',
    group: 'Editing',
    keys: ['backspace', 'delete'],
  },
  { action: 'selectAll', label: 'Select every node', group: 'Selection', keys: ['mod+a'] },
  { action: 'deselect', label: 'Clear the selection', group: 'Selection', keys: ['escape'] },
  { action: 'fitView', label: 'Fit the workflow in view', group: 'View', keys: ['shift+1'] },
  { action: 'zoomIn', label: 'Zoom in', group: 'View', keys: ['plus', '='] },
  { action: 'zoomOut', label: 'Zoom out', group: 'View', keys: ['-'] },
  { action: 'save', label: 'Save', group: 'Workflow', keys: ['mod+s'] },
  { action: 'run', label: 'Run', group: 'Workflow', keys: ['mod+enter'] },
  { action: 'search', label: 'Search the node list', group: 'Workflow', keys: ['/'] },
  { action: 'help', label: 'List the shortcuts', group: 'Workflow', keys: ['?'] },
]

/** The ones that stay as they are: the browser's own copy and paste, and the arrows. */
export const fixedShortcuts: { label: string; group: ShortcutGroup; keys: string[] }[] = [
  { label: 'Copy the selected nodes', group: 'Editing', keys: ['mod+c'] },
  { label: 'Cut the selected nodes', group: 'Editing', keys: ['mod+x'] },
  { label: 'Paste nodes', group: 'Editing', keys: ['mod+v'] },
  {
    label: 'Move the selected nodes',
    group: 'Selection',
    keys: ['arrowup', 'arrowdown', 'arrowleft', 'arrowright'],
  },
  { label: 'Move them further', group: 'Selection', keys: ['shift+arrowup'] },
]

export const shortcutGroups: ShortcutGroup[] = ['Editing', 'Selection', 'View', 'Workflow']

/** The keys of each action: the user's where they set one, else those to start with. */
export type Keymap = Record<ShortcutAction, string[]>

export function keymapOf(own: Record<string, string> | undefined): Keymap {
  return Object.fromEntries(
    shortcuts.map((s) => [s.action, own?.[s.action] ? [own[s.action]] : s.keys]),
  ) as Keymap
}

const modifiers = ['mod', 'ctrl', 'alt', 'shift'] as const

export const isMac =
  typeof navigator !== 'undefined' &&
  /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)

/** A key's name in a binding, from a key press; empty for a modifier alone. */
function keyOf(e: Pick<KeyboardEvent, 'key' | 'code'>): string {
  // Letters and digits by the key, else by where it is, so a Cyrillic layout's Ctrl+Z
  // is still Ctrl+Z; a digit by where it is, as Shift+1 gives ! on most layouts.
  if (/^Digit\d$/.test(e.code)) return e.code.slice(5)
  if (/^[a-z]$/i.test(e.key)) return e.key.toLowerCase()
  if (/^Key[A-Z]$/.test(e.code)) return e.code.slice(3).toLowerCase()

  switch (e.key) {
    case 'Control':
    case 'Meta':
    case 'Alt':
    case 'Shift':
      return ''
    case '+':
      return 'plus'
    case ' ':
      return 'space'
  }

  return e.key.toLowerCase()
}

// Symbols that take Shift on a keyboard, such as ? and +: the character says it all.
const shifted = (key: string) => key.length === 1 && !/[a-z0-9]/.test(key)

/** The binding a key press makes, such as mod+shift+z; empty for a modifier alone. */
export function bindingOf(
  e: Pick<KeyboardEvent, 'key' | 'code' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>,
  mac = isMac,
): string {
  const key = keyOf(e)
  if (!key) return ''

  const parts: string[] = []
  if (mac ? e.metaKey : e.ctrlKey) parts.push('mod')
  if (mac && e.ctrlKey) parts.push('ctrl')
  if (e.altKey) parts.push('alt')
  if (e.shiftKey && !shifted(key) && key !== 'plus') parts.push('shift')

  return [...parts, key].join('+')
}

/** The action a key press is, if any. */
export function actionOf(
  e: KeyboardEvent,
  keymap: Keymap,
  mac = isMac,
): ShortcutAction | undefined {
  const b = bindingOf(e, mac)
  if (!b) return undefined

  return shortcuts.find((s) => keymap[s.action].includes(b))?.action
}

/** A binding as a keyboard shows it: ⌘⇧Z on a Mac, Ctrl+Shift+Z elsewhere. */
export function shown(binding: string, mac = isMac): string {
  const names: Record<string, [string, string]> = {
    mod: ['⌘', 'Ctrl'],
    ctrl: ['⌃', 'Ctrl'],
    alt: ['⌥', 'Alt'],
    shift: ['⇧', 'Shift'],
    enter: ['↵', 'Enter'],
    escape: ['Esc', 'Esc'],
    backspace: ['⌫', 'Backspace'],
    delete: ['⌦', 'Delete'],
    arrowup: ['↑', '↑'],
    arrowdown: ['↓', '↓'],
    arrowleft: ['←', '←'],
    arrowright: ['→', '→'],
    plus: ['+', '+'],
    space: ['Space', 'Space'],
    tab: ['Tab', 'Tab'],
  }

  const parts = binding.split('+').map((p) => {
    const name = names[p]
    if (name) return mac ? name[0] : name[1]
    return p.length === 1 ? p.toUpperCase() : p[0].toUpperCase() + p.slice(1)
  })

  return parts.join(mac ? '' : '+')
}

/** Why a binding cannot be one, if it cannot: none, a modifier alone, or a browser's own. */
export function bindingProblem(binding: string): string | undefined {
  const parts = binding.split('+')
  const key = parts.at(-1) ?? ''

  if (!key || (modifiers as readonly string[]).includes(key))
    return 'Press a key, with or without Cmd, Ctrl, Alt or Shift'
  if (['mod+c', 'mod+x', 'mod+v'].includes(binding)) return 'Copy, cut and paste keep their keys'
  if (['mod+w', 'mod+t', 'mod+n', 'mod+q', 'mod+r', 'mod+l'].includes(binding))
    return 'The browser keeps that one'
  if (key.startsWith('arrow') && parts.length <= 2) return 'The arrows move the selected nodes'

  return undefined
}

/** The action other than this one that has binding, if one has. */
export function takenBy(
  binding: string,
  action: ShortcutAction,
  keymap: Keymap,
): Shortcut | undefined {
  return shortcuts.find((s) => s.action !== action && keymap[s.action].includes(binding))
}

/** Whether a key press comes from somewhere typing goes, where keys are the field's. */
export function inField(target: EventTarget | null): boolean {
  return (
    target instanceof Element &&
    target.closest(
      'input, textarea, select, [contenteditable], .nokey, [role="dialog"], [role="alertdialog"], [role="menu"]',
    ) !== null
  )
}
