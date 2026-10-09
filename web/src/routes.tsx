import { Navigate, type RouteObject } from 'react-router'

import { AppShell } from '@/components/AppShell'
import { RequireAuth } from '@/components/RequireAuth'
import { Skeleton } from '@/components/ui/skeleton'
import { LoginPage } from '@/pages/LoginPage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { RegisterPage } from '@/pages/RegisterPage'
import { WorkflowsPage } from '@/pages/WorkflowsPage'

export const routes: RouteObject[] = [
  { path: '/login', element: <LoginPage /> },
  { path: '/register', element: <RegisterPage /> },
  {
    element: <RequireAuth />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <Navigate to="/workflows" replace /> },
          { path: 'workflows', element: <WorkflowsPage /> },
          {
            path: 'workflows/:id',
            // The canvas library is most of the bundle; only this page needs it.
            lazy: async () => ({ Component: (await import('@/pages/WorkflowPage')).WorkflowPage }),
            // Shown inside the shell while the page loads on a fresh visit.
            hydrateFallbackElement: (
              <div className="p-6">
                <Skeleton className="h-96" />
              </div>
            ),
          },
        ],
      },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
]
