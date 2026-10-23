import { Navigate, type RouteObject } from 'react-router'

import { AppShell } from '@/components/AppShell'
import { RequireAuth } from '@/components/RequireAuth'
import { Skeleton } from '@/components/ui/skeleton'
import { AdminConfigPage } from '@/pages/AdminConfigPage'
import { AdminInvitesPage } from '@/pages/AdminInvitesPage'
import { AdminPluginsPage } from '@/pages/AdminPluginsPage'
import { AdminPage } from '@/pages/AdminPage'
import { CustomNodePage } from '@/pages/CustomNodePage'
import { ForgotPasswordPage } from '@/pages/ForgotPasswordPage'
import { LibraryPage } from '@/pages/LibraryPage'
import { LoginPage } from '@/pages/LoginPage'
import { NodesPage } from '@/pages/NodesPage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { OrdersPage } from '@/pages/OrdersPage'
import { RegisterPage } from '@/pages/RegisterPage'
import { ResetPasswordPage } from '@/pages/ResetPasswordPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { VerifyEmailPage } from '@/pages/VerifyEmailPage'
import { WalletsPage } from '@/pages/WalletsPage'
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
  { path: '/verify-email', element: <VerifyEmailPage /> },
  { path: '/forgot-password', element: <ForgotPasswordPage /> },
  { path: '/reset-password', element: <ResetPasswordPage /> },
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
          { path: 'library', element: <LibraryPage /> },
          {
            path: 'library/:id',
            lazy: async () => ({ Component: (await import('@/pages/TemplatePage')).TemplatePage }),
            hydrateFallbackElement: editor.hydrateFallbackElement,
          },
          { path: 'nodes', element: <NodesPage /> },
          { path: 'nodes/new', element: <CustomNodePage /> },
          { path: 'nodes/:id', element: <CustomNodePage /> },
          { path: 'wallets', element: <WalletsPage /> },
          { path: 'orders', element: <OrdersPage /> },
          // A page of Settings now (D392): old links still get there.
          { path: 'credentials', element: <Navigate to="/settings/credentials" replace /> },
          { path: 'settings/:page?', element: <SettingsPage /> },
          { path: 'admin', element: <AdminPage /> },
          { path: 'admin/invites', element: <AdminInvitesPage /> },
          { path: 'admin/config/:page?', element: <AdminConfigPage /> },
          { path: 'admin/plugins', element: <AdminPluginsPage /> },
        ],
      },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
]
