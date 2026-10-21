import { lazy, Suspense, type ReactNode } from 'react'

import type { ParamSpec } from '@/api/client'
import type { Shape } from '@/features/workflow/shapes'

/** What the expressions of a setting see: the items there, and the node's settings. */
export type ExpressionScope = {
  item: Shape
  settings: readonly ParamSpec[]
  items?: boolean // the node sees items: every item it receives (D257)
}

export type ExpressionInputProps = {
  id: string
  value: string
  onChange: (value: string) => void
  label: string
  scope: ExpressionScope
  placeholder?: string
  invalid?: boolean
  describedBy?: string
  multiline?: boolean
  rows?: number
  /** The plain input, shown while Monaco loads and in tests. */
  fallback: ReactNode
}

const ExpressionEditor = lazy(() => import('./ExpressionEditor'))

/**
 * A setting's text in Monaco: plain outside {{ }}, TypeScript inside, completed from the
 * items' fields, params, execution and dec, and checked as you type (D67, D68).
 */
export function ExpressionInput(props: ExpressionInputProps) {
  return (
    <Suspense fallback={props.fallback}>
      <ExpressionEditor {...props} />
    </Suspense>
  )
}
