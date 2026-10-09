import { X } from 'lucide-react'
import type { ReactNode } from 'react'

import type { NodeType, WorkflowNode } from '@/api/client'
import { Button } from '@/components/ui/button'

type Props = {
  spec: WorkflowNode
  type?: NodeType
  onClose: () => void
}

/** The selected node's settings, read-only. */
export function NodeDetails({ spec, type, onClose }: Props) {
  const declared = type?.params ?? []
  const configured = spec.params ?? {}
  const unknown = Object.keys(configured).filter((name) => !declared.some((p) => p.name === name))

  return (
    <aside className="w-80 shrink-0 overflow-y-auto border-l bg-background p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="truncate font-medium">{spec.name || type?.label || spec.type}</h2>
          <p className="text-xs text-muted-foreground">{spec.type}</p>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="Close" onClick={onClose}>
          <X />
        </Button>
      </div>

      {type?.description && (
        <p className="mt-3 text-sm text-muted-foreground">{type.description}</p>
      )}

      <dl className="mt-4 space-y-3 text-sm">
        {declared.map((p) => (
          <Setting key={p.name} label={p.label || p.name} hint={p.description}>
            {p.name in configured ? (
              format(configured[p.name])
            ) : p.default !== undefined ? (
              <span className="text-muted-foreground">{format(p.default)} (default)</span>
            ) : (
              <span className="text-muted-foreground">Not set</span>
            )}
          </Setting>
        ))}

        {unknown.map((name) => (
          <Setting key={name} label={name} hint="Not a parameter of this node type">
            {format(configured[name])}
          </Setting>
        ))}

        {spec.continueOnFail && (
          <Setting label="On failure">Continue; failed items go to the error port</Setting>
        )}
      </dl>
    </aside>
  )
}

function Setting({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div>
      <dt className="font-medium">{label}</dt>
      {hint && <dd className="text-xs text-muted-foreground">{hint}</dd>}
      <dd className="mt-0.5 break-words">{children}</dd>
    </div>
  )
}

function format(value: unknown): string {
  if (value === undefined || value === null || value === '') return '—'
  if (Array.isArray(value)) return value.map(String).join(', ')
  if (typeof value === 'object') return JSON.stringify(value)

  return String(value)
}
