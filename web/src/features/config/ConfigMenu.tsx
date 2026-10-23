import { SideMenu } from '@/components/SideMenu'

import { configPath, historySlug, matches, type ConfigPage } from './pages'

type Props = {
  pages: ConfigPage[]
  current: string // the open page's slug, empty on the overview
  unsaved: (page: ConfigPage) => number
}

/**
 * The Config pages' menu (D370, D391), History last: its search finds a setting across
 * the pages by label, key and description.
 */
export function ConfigMenu({ pages, current, unsaved }: Props) {
  return (
    <SideMenu
      label="Config pages"
      pages={[
        ...pages.map((p) => ({ slug: p.slug, label: p.label, unsaved: unsaved(p) })),
        { slug: historySlug, label: 'History', apart: true },
      ]}
      current={current}
      path={configPath}
      find={(query) =>
        pages
          .map((p) => ({
            slug: p.slug,
            label: p.label,
            items: p.settings
              .filter((s) => matches(s, query))
              .map((s) => ({ key: s.key, label: s.label })),
          }))
          .filter((found) => found.items.length > 0)
      }
    />
  )
}
