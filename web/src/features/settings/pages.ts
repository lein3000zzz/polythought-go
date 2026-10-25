import type { Found } from '@/components/SideMenu'

/**
 * The Settings pages (D379, D392): an overview at /settings, then a page each at
 * /settings/<slug>. A thing on a page is #<key> there, its element jumpId(key).
 */
export const settingsPages = [
  { slug: 'account', label: 'Account' },
  { slug: 'telegram', label: 'Telegram' },
  { slug: 'notifications', label: 'Notifications' },
  { slug: 'agents', label: 'AI agents' },
  { slug: 'tokens', label: 'API tokens' },
  { slug: 'connected', label: 'Connected agents' },
  { slug: 'credentials', label: 'Credentials' },
  { slug: 'shortcuts', label: 'Shortcuts' },
]

export function settingsPath(slug?: string, key?: string): string {
  return `/settings${slug ? `/${slug}` : ''}${key ? `#${key}` : ''}`
}

export const jumpPrefix = 'jump-'

export function jumpId(key: string): string {
  return jumpPrefix + key
}

/** Something the search finds, by its label and the words given. */
export type Entry = { slug: string; key: string; label: string; words: string }

/** The entries every word of the query is in, by page, in the pages' order. */
export function matching(entries: Entry[], query: string, pages: typeof settingsPages): Found[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean)
  const found = entries.filter((e) => {
    const text = `${e.label} ${e.words}`.toLowerCase()
    return words.every((w) => text.includes(w))
  })

  return pages
    .map((p) => ({
      slug: p.slug,
      label: p.label,
      items: found.filter((e) => e.slug === p.slug).map((e) => ({ key: e.key, label: e.label })),
    }))
    .filter((f) => f.items.length > 0)
}
