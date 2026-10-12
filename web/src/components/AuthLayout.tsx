import type { ReactNode } from 'react'

import { ThemeToggle } from '@/components/ThemeToggle'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

type Props = {
  title: string
  description?: string
  children: ReactNode
}

/** The centred card the signed-out pages share. */
export function AuthLayout({ title, description, children }: Props) {
  return (
    <div className="relative flex min-h-dvh flex-col items-center justify-center gap-6 bg-muted/40 p-4">
      <div className="absolute top-3 right-3">
        <ThemeToggle />
      </div>
      <span className="font-heading text-lg font-semibold">Polythought</span>
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>{title}</CardTitle>
          {description && <CardDescription>{description}</CardDescription>}
        </CardHeader>
        <CardContent>{children}</CardContent>
      </Card>
    </div>
  )
}
