import type { NodeType, WorkflowNode } from '@/api/client'

import { textOf } from './params'

type Param = NodeType['params'][number]

type Props = {
  node: WorkflowNode
  type: NodeType
  /** What a setting left unset, with no default, says. */
  unset?: (param: Param) => string
}

/** A node's settings, read-only, as its type lists them: as set, its default, or unset. */
export function NodeSettings({ node, type, unset = () => '—' }: Props) {
  const params = node.params ?? {}

  if (type.params.length === 0) return null

  return (
    <dl className="flex flex-col gap-2">
      {type.params.map((p) => (
        <div key={p.name}>
          <dt className="text-xs text-muted-foreground">{p.label ?? p.name}</dt>
          <dd className="font-mono text-xs break-words">
            {p.name in params
              ? hidden(params[p.name])
                ? 'Hidden: a personal setting'
                : textOf(params[p.name]).replace(/\n/g, ', ')
              : p.default !== undefined
                ? `${textOf(p.default)} (default)`
                : unset(p)}
          </dd>
        </div>
      ))}
    </dl>
  )
}

/** A personal setting's value, as one not shown stands (D402). */
function hidden(v: unknown): boolean {
  return (
    typeof v === 'object' &&
    v !== null &&
    Object.keys(v).length === 1 &&
    (v as Record<string, unknown>).$personal === 'hidden'
  )
}
