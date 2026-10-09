import { Navigate, type RouteObject } from 'react-router'

import { AppShell } from '@/components/AppShell'
import { RequireAuth } from '@/components/RequireAuth'
import { Skeleton } from '@/components/ui/skeleton'
import { LoginPage } from '@/pages/LoginPage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { RegisterPage } from '@/pages/RegisterPage'
import { WorkflowsPage } from '@/pages/WorkflowsPage'

// The editor and its canvas library are most of the bundle; only these pages need them.
const editor: RouteObject = {
  lazy: async () => ({
    Component: (await import('@/pages/WorkflowEditorPage')).WorkflowEditorPage,
  }),
  // Shown inside the shell while the page loads on a fresh visit.
  hydrateFallbackElement: (
    <div className="p-6">
      <Skeleton className="h-96" />
    </div>
  ),
}

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
          { path: 'workflows/new', ...editor },
          { path: 'workflows/:id', ...editor },
        ],
      },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
]
