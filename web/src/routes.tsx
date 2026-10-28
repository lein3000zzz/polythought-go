import { Navigate, type RouteObject } from 'react-router'

import { AdminLayout } from '@/components/AdminLayout'
import { AppShell, type RouteHandle } from '@/components/AppShell'
import { RequireAuth } from '@/components/RequireAuth'
import { Skeleton } from '@/components/ui/skeleton'
import { AdminConfigPage } from '@/pages/AdminConfigPage'
import { AdminInvitesPage } from '@/pages/AdminInvitesPage'
import { AdminPluginsPage } from '@/pages/AdminPluginsPage'
import { AdminPage } from '@/pages/AdminPage'
import { AdminStatsPage } from '@/pages/AdminStatsPage'
import { AdminUserStatsPage } from '@/pages/AdminUserStatsPage'
import { ConfirmationPage } from '@/pages/ConfirmationPage'
import { CustomNodePage } from '@/pages/CustomNodePage'
import { DashboardPage } from '@/pages/DashboardPage'
import { ForgotPasswordPage } from '@/pages/ForgotPasswordPage'
import { LibraryPage } from '@/pages/LibraryPage'
import { LoginPage } from '@/pages/LoginPage'
import { NodesPage } from '@/pages/NodesPage'
import { NotFoundPage } from '@/pages/NotFoundPage'
import { OAuthAuthorizePage } from '@/pages/OAuthAuthorizePage'
import { OrdersPage } from '@/pages/OrdersPage'
import { RegisterPage } from '@/pages/RegisterPage'
import { RequestAccessPage } from '@/pages/RequestAccessPage'
import { ResetPasswordPage } from '@/pages/ResetPasswordPage'
import { SettingsPage } from '@/pages/SettingsPage'
import { VerifyEmailPage } from '@/pages/VerifyEmailPage'
import { WalletsPage } from '@/pages/WalletsPage'
import { WorkflowsPage } from '@/pages/WorkflowsPage'

// The editor and its canvas library are most of the bundle; only these pages need them.
const editor: RouteObject = {
  handle: { fullBleed: true } satisfies RouteHandle,
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
  { path: '/request-access', element: <RequestAccessPage /> },
  { path: '/forgot-password', element: <ForgotPasswordPage /> },
  { path: '/reset-password', element: <ResetPasswordPage /> },
  {
    element: <RequireAuth />,
    children: [
      // Where agents send their users to allow them (D384): a page of its own, as sign-in's.
      { path: 'oauth/authorize', element: <OAuthAuthorizePage /> },
      {
        element: <AppShell />,
        children: [
          { index: true, element: <Navigate to="/workflows" replace /> },
          { path: 'workflows', element: <WorkflowsPage /> },
          { path: 'workflows/dashboard', element: <DashboardPage /> },
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
          // Where an agent's window links, to confirm one of its live actions (D409).
          { path: 'confirmations/:id', element: <ConfirmationPage /> },
          // A page of Settings now (D392): old links still get there.
          { path: 'credentials', element: <Navigate to="/settings/credentials" replace /> },
          { path: 'settings/:page?', element: <SettingsPage /> },
          {
            path: 'admin',
            element: <AdminLayout />,
            children: [
              { index: true, element: <AdminPage /> },
              { path: 'stats', element: <AdminStatsPage /> },
              { path: 'stats/:id', element: <AdminUserStatsPage /> },
              { path: 'invites', element: <AdminInvitesPage /> },
              { path: 'config/:page?', element: <AdminConfigPage /> },
              { path: 'plugins', element: <AdminPluginsPage /> },
            ],
          },
        ],
      },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
]
