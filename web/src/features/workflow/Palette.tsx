import { Box, Zap } from 'lucide-react'

import type { NodeType } from '@/api/client'

/** The drag-and-drop payload: the node type's name. */
export const nodeTypeMime = 'application/x-polythought-node-type'

const groupNames: Record<string, string> = {
  polymarket: 'Polymarket',
  logic: 'Logic',
  transform: 'Transform',
  output: 'Output',
  telegram: 'Telegram',
}

// Groups follow the way items flow: where they start, come from, are sorted out, go.
const groupOrder = ['Triggers', 'Polymarket', 'Logic', 'Transform', 'Output', 'Telegram', 'Other']

type Props = {
  types: readonly NodeType[]
  onAdd: (type: NodeType) => void
}

/** The node types to add: click to place one in view, or drag it to a spot. */
export function Palette({ types, onAdd }: Props) {
  return (
    <nav aria-label="Node types" className="flex flex-col gap-4 p-3">
      {groups(types).map(([group, members]) => (
        <section key={group} className="flex flex-col gap-1">
          <h2 className="px-2 text-xs font-medium text-muted-foreground">{group}</h2>
          {members.map((t) => {
            const Icon = t.trigger ? Zap : Box

            return (
              <button
                key={t.name}
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
                    <span className="line-clamp-2 text-xs text-muted-foreground">
                      {t.description}
                    </span>
                  )}
                </span>
              </button>
            )
          })}
        </section>
      ))}
    </nav>
  )
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
