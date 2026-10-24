import { BookOpen, ChevronDown, LogOut, Menu } from 'lucide-react'
import { Link, NavLink, Outlet, useLocation, useMatches } from 'react-router'
import { cn } from 'cn'

import { useLogout, useMe } from '@/api/auth'
import type { Schemas } from '@/api/client'
import { useLiveEvents } from '@/api/events'
import { ConfirmationsBanner } from '@/components/ConfirmationsBanner'
import { GitHubLink } from '@/components/GitHubLink'
import { StopLiveButton, StoppedBanner } from '@/components/KillSwitch'
import { ThemeChoices } from '@/components/ThemeToggle'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Toaster } from '@/components/ui/sonner'

/** The pages in the bar itself (D368). */
const mainLinks = [
  ['/workflows', 'Workflows'],
  ['/library', 'Library'],
  ['/nodes', 'Nodes'],
] as const

/** The pages under its Trading menu. */
const tradingLinks = [
  ['/wallets', 'Wallets'],
  ['/orders', 'Live orders'],
] as const

const linkClass = 'text-muted-foreground hover:text-foreground'

/** What a route says of itself to the shell: fullBleed for a canvas that fills the page. */
export type RouteHandle = { fullBleed?: boolean }

export function AppShell() {
  const { data: user } = useMe()
  useLiveEvents()
  const fullBleed = useMatches().some((m) => (m.handle as RouteHandle | undefined)?.fullBleed)

  return (
    <div className="flex h-dvh flex-col">
      <header className="flex h-14 shrink-0 items-center gap-6 border-b px-4">
        <Link to="/" className="font-heading font-semibold">
          Polythought
        </Link>

        <nav aria-label="Pages" className="hidden items-center gap-4 text-sm md:flex">
          {mainLinks.map(([to, label]) => (
            <NavLink
              key={to}
              to={to}
              className={({ isActive }) => cn(linkClass, isActive && 'text-foreground')}
            >
              {label}
            </NavLink>
          ))}
          <TradingMenu />
        </nav>

        <div className="ml-auto flex items-center gap-4 text-sm">
          {user && <StopLiveButton user={user} />}
          <PagesMenu />
          {/* Outside the app's router; a new tab keeps unsaved work in the editor. */}
          <a
            href="/docs/"
            target="_blank"
            rel="noopener"
            className={cn('flex items-center gap-1', linkClass)}
          >
            <BookOpen className="size-4" />
            Docs
          </a>
          <GitHubLink />
          {user && <AccountMenu user={user} />}
        </div>
      </header>

      {user && <StoppedBanner user={user} />}
      {user && <ConfirmationsBanner />}

      {/* The scrollbar's room is kept, so a page that scrolls does not move sideways from
          one that does not; but not beside a canvas that fills the page and never scrolls. */}
      <main
        className={cn('min-h-0 flex-1 overflow-auto', !fullBleed && '[scrollbar-gutter:stable]')}
      >
        <Outlet />
      </main>

      <Toaster position="bottom-right" />
    </div>
  )
}

/** Wallets and Live orders, its trigger marked while one of them is open. */
function TradingMenu() {
  const { pathname } = useLocation()
  const here = tradingLinks.some(([to]) => pathname === to || pathname.startsWith(`${to}/`))

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className={cn(
          'flex items-center gap-0.5 outline-none',
          linkClass,
          here && 'text-foreground',
        )}
      >
        Trading
        <ChevronDown className="size-3.5" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <MenuLinks links={tradingLinks} />
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** Every page of the bar, in one menu, where the screen is too narrow for them (D368). */
function PagesMenu() {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" className="md:hidden" aria-label="Pages">
          <Menu />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <MenuLinks links={mainLinks} />
        <DropdownMenuSeparator />
        <DropdownMenuLabel>Trading</DropdownMenuLabel>
        <MenuLinks links={tradingLinks} />
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function MenuLinks({ links }: { links: readonly (readonly [string, string])[] }) {
  const { pathname } = useLocation()

  return links.map(([to, label]) => {
    const here = pathname === to || pathname.startsWith(`${to}/`)

    return (
      <DropdownMenuItem key={to} asChild>
        <Link
          to={to}
          aria-current={here ? 'page' : undefined}
          className={cn(here && 'font-medium')}
        >
          {label}
        </Link>
      </DropdownMenuItem>
    )
  })
}

/** Who is signed in, their pages, the theme, and signing out (D368). */
function AccountMenu({ user }: { user: Schemas['User'] }) {
  const logout = useLogout()
  const admin = user.role === 'admin'
  const own: (readonly [string, string])[] = [
    ['/settings', 'Settings'],
    ['/settings/credentials', 'Credentials'],
    ...(admin ? ([['/admin', 'Admin']] as const) : []),
  ]

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          className="rounded-full"
          aria-label="Account"
          title={user.email}
        >
          <span className="flex size-7 items-center justify-center rounded-full bg-muted text-xs font-medium uppercase">
            {user.email.charAt(0)}
          </span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        <DropdownMenuLabel className="flex flex-col gap-1 font-normal">
          <span className="truncate">{user.email}</span>
          {admin && (
            <Badge variant="secondary" className="w-fit">
              Admin
            </Badge>
          )}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <MenuLinks links={own} />
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="text-xs text-muted-foreground">Theme</DropdownMenuLabel>
        <ThemeChoices />
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => logout.mutate()} disabled={logout.isPending}>
          <LogOut />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
