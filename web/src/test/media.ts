// A stand-in for matchMedia, which jsdom lacks. Only the system colour scheme matters to
// the app; tests flip it with setSystemDark.

const darkQuery = '(prefers-color-scheme: dark)'
const listeners = new Set<(e: MediaQueryListEvent) => void>()
let systemDark = false

export function setSystemDark(dark: boolean) {
  systemDark = dark
  listeners.forEach((l) => l({ matches: dark, media: darkQuery } as MediaQueryListEvent))
}

window.matchMedia = (query: string) =>
  ({
    media: query,
    get matches() {
      return query === darkQuery && systemDark
    },
    addEventListener: (_: string, l: (e: MediaQueryListEvent) => void) => listeners.add(l),
    removeEventListener: (_: string, l: (e: MediaQueryListEvent) => void) => listeners.delete(l),
  }) as unknown as MediaQueryList
