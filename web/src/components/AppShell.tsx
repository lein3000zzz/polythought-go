import { LogOut } from 'lucide-react'
import { Link, NavLink, Outlet } from 'react-router'
import { cn } from 'cn'

import { useLogout, useMe } from '@/api/auth'
import { useLiveEvents } from '@/api/events'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Toaster } from '@/components/ui/sonner'

const navLinks = [
  ['/workflows', 'Workflows'],
  ['/library', 'Library'],
  ['/wallets', 'Wallets'],
  ['/settings', 'Settings'],
] as const

const adminLinks = [['/admin', 'Admin']] as const

export function AppShell() {
  const { data: user } = useMe()
  const logout = useLogout()
  useLiveEvents()

  return (
    <div className="flex h-dvh flex-col">
      <header className="flex h-14 shrink-0 items-center gap-6 border-b px-4">
        <Link to="/" className="font-heading font-semibold">
          Polythought
        </Link>

        <nav className="flex gap-4 text-sm">
          {[...navLinks, ...(user?.role === 'admin' ? adminLinks : [])].map(([to, label]) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) =>
                cn('text-muted-foreground hover:text-foreground', isActive && 'text-foreground')
              }
            >
              {label}
            </NavLink>
          ))}
        </nav>

        <div className="ml-auto flex items-center gap-3 text-sm">
          {user && <span className="text-muted-foreground">{user.email}</span>}
          {user?.role === 'admin' && <Badge variant="secondary">Admin</Badge>}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => logout.mutate()}
            disabled={logout.isPending}
          >
            <LogOut />
            Sign out
          </Button>
        </div>
      </header>

      <main className="min-h-0 flex-1 overflow-auto">
        <Outlet />
      </main>

      <Toaster position="bottom-right" />
    </div>
  )
}
