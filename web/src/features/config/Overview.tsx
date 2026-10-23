import { PageCard } from '@/components/PageCard'
import { Badge } from '@/components/ui/badge'

import { configPath, historySlug, locked, type ConfigPage } from './pages'
import { PolymarketAccess } from './PolymarketAccess'

type Props = {
  pages: ConfigPage[]
  unsaved: (page: ConfigPage) => number
}

/** /admin/config itself (D391): Polymarket access, and each page with what is set there. */
export function Overview({ pages, unsaved }: Props) {
  return (
    <div className="flex flex-col gap-8">
      <PolymarketAccess />

      <section className="flex flex-col gap-3">
        <h2 className="font-medium">Pages</h2>
        <ul className="grid gap-3 sm:grid-cols-2">
          {pages.map((p) => {
            const saved = p.settings.filter((s) => s.source === 'database').length
            const fixed = p.settings.filter(locked).length
            const n = unsaved(p)

            return (
              <li key={p.slug}>
                <PageCard to={configPath(p.slug)} label={p.label}>
                  {[
                    `${p.settings.length} ${p.settings.length === 1 ? 'setting' : 'settings'}`,
                    saved > 0 && `${saved} saved`,
                    fixed > 0 && `${fixed} set by the environment or config file`,
                  ]
                    .filter(Boolean)
                    .join(' · ')}
                  {n > 0 && (
                    <Badge variant="outline" className="mt-1 self-start">
                      {n} unsaved
                    </Badge>
                  )}
                </PageCard>
              </li>
            )
          })}
          <li>
            <PageCard to={configPath(historySlug)} label="History">
              Every change made here, newest first, and who made it.
            </PageCard>
          </li>
        </ul>
      </section>
    </div>
  )
}
