import type { Setting } from '@/api/settings'

/**
 * The Config page's pages (D370, D391): an overview, a page for each group of settings in
 * the server's order, and History. Each page is /admin/config/<slug>; a setting is
 * #<key> on its page.
 */
export type ConfigPage = { slug: string; label: string; settings: Setting[] }

/** New values by setting key, across the pages. null resets a setting to its default. */
export type Drafts = Record<string, unknown>

export const historySlug = 'history'

/** A group's page slug: AI agents is ai-agents. */
export function slugOf(group: string): string {
  return group
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
}

export function configPath(slug?: string, key?: string): string {
  return `/admin/config${slug ? `/${slug}` : ''}${key ? `#${key}` : ''}`
}

/** The groups' pages, in the order the server lists their settings. */
export function groupPages(list: Setting[]): ConfigPage[] {
  const pages = new Map<string, ConfigPage>()

  for (const s of list) {
    const page = pages.get(s.group) ?? { slug: slugOf(s.group), label: s.group, settings: [] }
    page.settings.push(s)
    pages.set(s.group, page)
  }

  return [...pages.values()]
}

/** Whether every word of the query is in the setting's label, key, description or group. */
export function matches(s: Setting, query: string): boolean {
  const text = `${s.label} ${s.key} ${s.description} ${s.group}`.toLowerCase()

  return query
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((word) => text.includes(word))
}

/** Whether the environment or the config file sets it, which the page cannot change. */
export function locked(s: Setting): boolean {
  return s.source === 'environment' || s.source === 'file'
}

/** Whether a draft value would change what is saved. */
export function isChange(s: Setting, draft: unknown): boolean {
  if (locked(s)) return false
  if (draft === null) return s.secret ? s.isSet : s.source === 'database'

  return s.secret || JSON.stringify(draft) !== JSON.stringify(s.value)
}

/** Where a setting's row is, for a link to it: #<key> on its page. */
export const settingPrefix = 'setting-'

export function rowId(key: string): string {
  return settingPrefix + key
}
