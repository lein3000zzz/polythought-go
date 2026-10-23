import { NavLink } from 'react-router'
import { cn } from 'cn'

import { useJoinRequests } from '@/api/admin'

const tabs = [
  ['/admin', 'Users'],
  ['/admin/invites', 'Invites'],
  ['/admin/config', 'Config'],
  ['/admin/plugins', 'Plugins'],
] as const

/** The admin pages' title and tabs; Invites counts the requests to join waiting (D394). */
export function AdminHeader() {
  const waiting = useJoinRequests().data?.pending ?? 0

  return (
    <div className="flex flex-col gap-3">
      <h1 className="font-heading text-xl font-semibold">Admin</h1>
      <nav aria-label="Admin pages" className="flex gap-1 border-b">
        {tabs.map(([to, label]) => {
          const count = to === '/admin/invites' ? waiting : 0

          return (
            <NavLink
              key={to}
              to={to}
              end={to === '/admin'} // Config's pages are under it
              // Named in full: the count alone says nothing to a screen reader.
              aria-label={count > 0 ? `${label}, ${count} requests waiting` : undefined}
              className={({ isActive }) =>
                cn(
                  '-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm',
                  isActive
                    ? 'border-primary text-foreground'
                    : 'border-transparent text-muted-foreground hover:text-foreground',
                )
              }
            >
              {label}
              {count > 0 && (
                <span className="rounded-full bg-primary px-1.5 text-[10px] leading-4 font-medium text-primary-foreground tabular-nums">
                  {count}
                </span>
              )}
            </NavLink>
          )
        })}
      </nav>
    </div>
  )
}
