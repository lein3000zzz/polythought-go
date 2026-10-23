import { Outlet } from 'react-router'

import { useMe } from '@/api/auth'
import { AdminHeader } from '@/components/AdminHeader'

/**
 * The admin pages' frame: one width for all of them, and the tabs kept as one opens
 * another, so nothing moves as you go between them.
 */
export function AdminLayout() {
  const { data: me } = useMe()

  if (me?.role !== 'admin') {
    return <p className="p-6 text-sm text-muted-foreground">This page is for admins.</p>
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-8 p-6">
      <AdminHeader />
      <Outlet />
    </div>
  )
}
