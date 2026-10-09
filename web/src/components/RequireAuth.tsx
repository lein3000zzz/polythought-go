import { Navigate, Outlet, useLocation } from 'react-router'

import { useMe } from '@/api/auth'
import { ErrorAlert } from '@/components/ErrorAlert'

/** Renders its child routes for signed-in users and sends everyone else to sign in. */
export function RequireAuth() {
  const me = useMe()
  const location = useLocation()

  if (me.isPending) return null // usually a few milliseconds; a spinner would only flash

  if (me.isError) {
    return (
      <div className="mx-auto max-w-md p-6">
        <ErrorAlert error={me.error} onRetry={() => void me.refetch()} />
      </div>
    )
  }

  if (!me.data) {
    const next = encodeURIComponent(location.pathname + location.search)
    return <Navigate to={`/login?next=${next}`} replace />
  }

  return <Outlet />
}
