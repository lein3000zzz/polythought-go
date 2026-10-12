import { useSyncExternalStore } from 'react'

export type Theme = 'light' | 'dark'

/** What the user picked: a theme, or whatever their system uses. */
export type ThemePreference = Theme | 'system'

// public/theme.js reads the same key before the app loads, to paint the right theme.
export const themeStorageKey = 'polythought.theme'

const darkQuery = '(prefers-color-scheme: dark)'

type Snapshot = { preference: ThemePreference; theme: Theme }

function readPreference(): ThemePreference {
  try {
    const saved = localStorage.getItem(themeStorageKey)
    return saved === 'light' || saved === 'dark' ? saved : 'system'
  } catch {
    return 'system' // storage can be off, e.g. in some private windows
  }
}

function systemTheme(): Theme {
  return window.matchMedia?.(darkQuery).matches ? 'dark' : 'light'
}

function compute(): Snapshot {
  const preference = readPreference()
  return { preference, theme: preference === 'system' ? systemTheme() : preference }
}

function apply(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark')
}

const listeners = new Set<() => void>()
let snapshot = compute()
apply(snapshot.theme)

function refresh() {
  const next = compute()
  if (next.preference === snapshot.preference && next.theme === snapshot.theme) return

  snapshot = next
  apply(next.theme)
  listeners.forEach((l) => l())
}

// Follow the system when it switches, and other tabs when the user picks there.
window.matchMedia?.(darkQuery).addEventListener('change', refresh)
window.addEventListener('storage', (e) => {
  if (e.key === themeStorageKey || e.key === null) refresh()
})

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/** The theme in use, and what the user picked. */
export function useTheme(): Snapshot {
  return useSyncExternalStore(subscribe, () => snapshot)
}

export function setThemePreference(preference: ThemePreference) {
  try {
    if (preference === 'system') localStorage.removeItem(themeStorageKey)
    else localStorage.setItem(themeStorageKey, preference)
  } catch {
    // Not saved, but still applied for this page.
  }

  const theme = preference === 'system' ? systemTheme() : preference
  if (snapshot.preference === preference && snapshot.theme === theme) return

  snapshot = { preference, theme }
  apply(theme)
  listeners.forEach((l) => l())
}
