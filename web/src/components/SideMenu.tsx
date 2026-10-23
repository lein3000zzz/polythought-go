import { cn } from 'cn'
import { Search } from 'lucide-react'
import { useEffect, useEffectEvent, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router'

import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

/** A page of the menu; apart ones come after a line. */
export type MenuPage = { slug: string; label: string; unsaved?: number; apart?: boolean }

/** What a search found on a page: each item is #<key> there. */
export type Found = { slug: string; label: string; items: { key: string; label: string }[] }

type Props = {
  /** The menu's name, such as "Config pages". */
  label: string
  pages: MenuPage[] // after the overview, the page at ''
  current: string // the open page's slug, empty on the overview
  path: (slug?: string, key?: string) => string
  find: (query: string) => Found[]
}

const linkClass = 'flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted'

/**
 * Pages beside the page they open, as Config's and Settings' are (D391): a dropdown above
 * it on narrow screens; a dot marks a page with unsaved changes. Its search lists what
 * find finds while there is a query, each a link to its place; / gets there from
 * anywhere, Enter opens the first.
 */
export function SideMenu({ label, pages, current, path, find }: Props) {
  const [query, setQuery] = useState('')
  const search = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()
  const { hash } = useLocation()

  const results = query.trim() ? find(query) : []

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

  const unsavedNote = (p: MenuPage) => (p.unsaved ? ` (${p.unsaved} unsaved)` : '')

  return (
    <div className="flex flex-col gap-3 md:sticky md:top-6 md:w-56 md:shrink-0">
      <div className="flex gap-2">
        <NativeSelect
          aria-label="Page"
          className="md:hidden"
          value={current}
          onChange={(e) => void navigate(path(e.target.value || undefined))}
        >
          <NativeSelectOption value="">Overview</NativeSelectOption>
          {pages.map((p) => (
            <NativeSelectOption key={p.slug} value={p.slug}>
              {p.label + unsavedNote(p)}
            </NativeSelectOption>
          ))}
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
                const { slug, items } = results[0]
                void navigate(path(slug, items[0].key))
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
            {results.map(({ slug, label: page, items }) => (
              <div key={slug} className="flex flex-col">
                <p className="px-2 pb-1 text-xs font-medium text-muted-foreground">{page}</p>
                {items.map((item) => {
                  const here = current === slug && hash.slice(1) === item.key

                  return (
                    <Link
                      key={item.key}
                      to={path(slug, item.key)}
                      aria-current={here ? 'location' : undefined}
                      className={cn(linkClass, here && 'bg-muted font-medium')}
                    >
                      {item.label}
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
        <nav aria-label={label} className="hidden flex-col md:flex">
          <MenuLink to={path()} here={current === ''} label="Overview" />
          {pages.map((p) => (
            <div key={p.slug} className="contents">
              {p.apart && <div className="my-2 border-t" />}
              <MenuLink
                to={path(p.slug)}
                here={current === p.slug}
                label={p.label}
                unsaved={p.unsaved}
              />
            </div>
          ))}
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
