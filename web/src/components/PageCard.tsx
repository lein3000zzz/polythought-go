import type { ReactNode } from 'react'
import { Link } from 'react-router'

/** A page on an overview, with what is there (D391, D392). */
export function PageCard({
  to,
  label,
  children,
}: {
  to: string
  label: string
  children: ReactNode
}) {
  return (
    <Link to={to} className="flex h-full flex-col gap-1 rounded-lg border p-3 hover:bg-muted/50">
      <span className="font-medium">{label}</span>
      <span className="flex flex-col text-xs text-muted-foreground">{children}</span>
    </Link>
  )
}
