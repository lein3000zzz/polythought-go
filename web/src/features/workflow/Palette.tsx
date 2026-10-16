import { Box, Search, Zap } from 'lucide-react'
import { useState, type Ref } from 'react'

import type { NodeType } from '@/api/client'
import { Input } from '@/components/ui/input'

/** The drag-and-drop payload: the node type's name. */
export const nodeTypeMime = 'application/x-polythought-node-type'

const groupNames: Record<string, string> = {
  polymarket: 'Polymarket',
  logic: 'Logic',
  transform: 'Transform',
  output: 'Output',
  telegram: 'Telegram',
  code: 'Code',
}

// Groups follow the way items flow: where they start, come from, are sorted out, go.
const groupOrder = [
  'Triggers',
  'Polymarket',
  'Logic',
  'Transform',
  'Code',
  'Output',
  'Telegram',
  'Other',
]

type Props = {
  types: readonly NodeType[]
  onAdd: (type: NodeType) => void
  /** The search field, for a shortcut to focus it. */
  searchRef?: Ref<HTMLInputElement>
}

/**
 * The node types to add: click to place one in view, or drag it to a spot. A search
 * above narrows the list; Enter adds the first match.
 */
export function Palette({ types, onAdd, searchRef }: Props) {
  const [query, setQuery] = useState('')
  const shown = groups(types).flatMap(([group, members]) => {
    const matching = members.filter((t) => matches(t, group, query))
    return matching.length > 0 ? [[group, matching] as const] : []
  })
  const first = shown[0]?.[1][0]

  return (
    <div className="flex h-full flex-col">
      <div className="flex h-10 shrink-0 items-center border-b pr-10 pl-2">
        <div className="relative w-full">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            ref={searchRef}
            type="search"
            aria-label="Search nodes"
            placeholder="Search nodes"
            title="Press / to search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && first) {
                e.preventDefault()
                onAdd(first)
                setQuery('')
              } else if (e.key === 'Escape' && query) {
                e.preventDefault()
                setQuery('')
              }
            }}
            className="h-7 pr-7 pl-7 text-sm"
          />
          {!query && (
            <kbd className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 rounded border px-1 font-mono text-[10px] text-muted-foreground">
              /
            </kbd>
          )}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {shown.length === 0 && (
          <p className="p-4 text-sm text-muted-foreground">No nodes match “{query.trim()}”.</p>
        )}

        {shown.length > 0 && (
          <nav aria-label="Node types" className="flex flex-col gap-4 p-3">
            {shown.map(([group, members]) => (
              <section key={group} className="flex flex-col gap-1">
                <h2 className="px-2 text-xs font-medium text-muted-foreground">{group}</h2>
                {members.map((t) => (
                  <NodeButton key={t.name} type={t} onAdd={onAdd} />
                ))}
              </section>
            ))}
          </nav>
        )}
      </div>
    </div>
  )
}

function NodeButton({ type: t, onAdd }: { type: NodeType; onAdd: (type: NodeType) => void }) {
  const Icon = t.trigger ? Zap : Box

  return (
    <button
      type="button"
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData(nodeTypeMime, t.name)
        e.dataTransfer.effectAllowed = 'move'
      }}
      onClick={() => onAdd(t)}
      title={t.description}
      className="flex items-start gap-2 rounded-md px-2 py-1.5 text-left text-sm hover:bg-muted"
    >
      <Icon
        className={
          t.trigger
            ? 'mt-0.5 size-4 shrink-0 text-amber-500'
            : 'mt-0.5 size-4 shrink-0 text-muted-foreground'
        }
      />
      <span className="min-w-0">
        <span className="block font-medium">{t.label || t.name}</span>
        {t.description && (
          <span className="line-clamp-2 text-xs text-muted-foreground">{t.description}</span>
        )}
      </span>
    </button>
  )
}

/** Whether every word of the query is in the node's name, type, description or group. */
function matches(t: NodeType, group: string, query: string): boolean {
  const haystack = [t.label, t.name, t.description, group].join(' ').toLowerCase()

  return query
    .toLowerCase()
    .split(/\s+/)
    .every((word) => haystack.includes(word))
}

// groups sorts the types into groups by the first part of their name, triggers apart.
function groups(types: readonly NodeType[]): [string, NodeType[]][] {
  const out = new Map<string, NodeType[]>(groupOrder.map((g) => [g, []]))

  for (const t of types) {
    const prefix = t.name.split('.')[0]
    const group = t.trigger ? 'Triggers' : (groupNames[prefix] ?? 'Other')

    out.get(group)?.push(t)
  }

  return [...out].filter(([, members]) => members.length > 0)
}
