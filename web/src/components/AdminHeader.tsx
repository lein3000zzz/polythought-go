import { NavLink } from 'react-router'
import { cn } from 'cn'

const tabs = [
  ['/admin', 'Users'],
  ['/admin/config', 'Config'],
  ['/admin/plugins', 'Plugins'],
] as const

/** The admin pages' title and tabs. */
export function AdminHeader() {
  return (
    <div className="flex flex-col gap-3">
      <h1 className="font-heading text-xl font-semibold">Admin</h1>
      <nav aria-label="Admin pages" className="flex gap-1 border-b">
        {tabs.map(([to, label]) => (
          <NavLink
            key={to}
            to={to}
            end
            className={({ isActive }) =>
              cn(
                '-mb-px border-b-2 px-3 py-2 text-sm',
                isActive
                  ? 'border-primary text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground',
              )
            }
          >
            {label}
          </NavLink>
        ))}
      </nav>
    </div>
  )
}
