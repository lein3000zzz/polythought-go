import { cn } from 'cn'
import { Search } from 'lucide-react'
import { useEffect, useEffectEvent, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router'

import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { configPath, historySlug, matches, type ConfigPage } from './pages'

type Props = {
  pages: ConfigPage[]
  current: string // the open page's slug, empty on the overview
  unsaved: (page: ConfigPage) => number
}

const linkClass = 'flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted'

/**
 * The Config pages' menu, beside them, and a dropdown above them on narrow screens
 * (D370, D391): a dot marks a page with unsaved changes. Its search finds a setting
 * across the pages by label, key and description; / gets there from anywhere.
 */
export function ConfigMenu({ pages, current, unsaved }: Props) {
  const [query, setQuery] = useState('')
  const search = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()
  const { hash } = useLocation()

  const results = query.trim()
    ? pages
        .map((page) => ({ page, settings: page.settings.filter((s) => matches(s, query)) }))
        .filter((r) => r.settings.length > 0)
    : []

  const onSlash = useEffectEvent((e: KeyboardEvent) => {
    if (e.key !== '/' || e.metaKey || e.ctrlKey || e.altKey) return
    if (
      e.target instanceof Element &&
      e.target.closest('input, textarea, select, [contenteditable]')
    ) {
      return
    }

    e.preventDefault()
    search.current?.focus()
  })
  useEffect(() => {
    window.addEventListener('keydown', onSlash)
    return () => window.removeEventListener('keydown', onSlash)
  }, [])

  const unsavedNote = (page: ConfigPage) => {
    const n = unsaved(page)
    return n > 0 ? ` (${n} unsaved)` : ''
  }

  return (
    <div className="flex flex-col gap-3 md:sticky md:top-6 md:w-56 md:shrink-0">
      <div className="flex gap-2">
        <NativeSelect
          aria-label="Page"
          className="md:hidden"
          value={current}
          onChange={(e) => void navigate(configPath(e.target.value || undefined))}
        >
          <NativeSelectOption value="">Overview</NativeSelectOption>
          {pages.map((p) => (
            <NativeSelectOption key={p.slug} value={p.slug}>
              {p.label + unsavedNote(p)}
            </NativeSelectOption>
          ))}
          <NativeSelectOption value={historySlug}>History</NativeSelectOption>
        </NativeSelect>

        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            ref={search}
            type="search"
            aria-label="Search settings"
            placeholder="Search settings"
            className="h-8 pr-2 pl-7 md:pr-7"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') setQuery('')
              if (e.key === 'Enter' && results.length > 0) {
                const { page, settings } = results[0]
                void navigate(configPath(page.slug, settings[0].key))
              }
            }}
          />
          {!query && (
            <kbd className="pointer-events-none absolute top-1/2 right-2 hidden -translate-y-1/2 rounded border px-1 text-[10px] text-muted-foreground md:block">
              /
            </kbd>
          )}
        </div>
      </div>

      {query.trim() ? (
        results.length > 0 ? (
          <nav aria-label="Matching settings" className="flex flex-col gap-3">
            {results.map(({ page, settings }) => (
              <div key={page.slug} className="flex flex-col">
                <p className="px-2 pb-1 text-xs font-medium text-muted-foreground">{page.label}</p>
                {settings.map((s) => {
                  const here = current === page.slug && hash.slice(1) === s.key

                  return (
                    <Link
                      key={s.key}
                      to={configPath(page.slug, s.key)}
                      aria-current={here ? 'location' : undefined}
                      className={cn(linkClass, here && 'bg-muted font-medium')}
                    >
                      {s.label}
                    </Link>
                  )
                })}
              </div>
            ))}
          </nav>
        ) : (
          <p className="px-2 text-sm text-muted-foreground">No settings match “{query.trim()}”.</p>
        )
      ) : (
        <nav aria-label="Config pages" className="hidden flex-col md:flex">
          <MenuLink to={configPath()} here={current === ''} label="Overview" />
          {pages.map((p) => (
            <MenuLink
              key={p.slug}
              to={configPath(p.slug)}
              here={current === p.slug}
              label={p.label}
              unsaved={unsaved(p)}
            />
          ))}
          <div className="my-2 border-t" />
          <MenuLink to={configPath(historySlug)} here={current === historySlug} label="History" />
        </nav>
      )}
    </div>
  )
}

type LinkProps = { to: string; here: boolean; label: string; unsaved?: number }

function MenuLink({ to, here, label, unsaved = 0 }: LinkProps) {
  const note = unsaved > 0 ? `${unsaved} unsaved` : undefined

  return (
    <Link
      to={to}
      aria-current={here ? 'page' : undefined}
      // Named in full: the dot alone says nothing to a screen reader.
      aria-label={note && `${label}, ${note}`}
      title={note}
      className={cn(linkClass, here ? 'bg-muted font-medium' : 'text-muted-foreground')}
    >
      {label}
      {note && <span aria-hidden className="ml-auto size-1.5 rounded-full bg-amber-500" />}
    </Link>
  )
}
