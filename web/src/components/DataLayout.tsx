import { NavLink, Outlet } from 'react-router'
import { cn } from 'cn'

const tabs = [
  ['/data/datasets', 'Datasets'],
  ['/data/models', 'Models'],
] as const

/** The Data pages' frame: their title and tabs, kept as one opens another. */
export function DataLayout() {
  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
      <div className="flex flex-col gap-3">
        <h1 className="font-heading text-xl font-semibold">Data</h1>
        <nav aria-label="Data pages" className="flex gap-1 border-b">
          {tabs.map(([to, label]) => (
            <NavLink
              key={to}
              to={to}
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
      <Outlet />
    </div>
  )
}
