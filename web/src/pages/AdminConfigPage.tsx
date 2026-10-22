import { useEffect, useState } from 'react'
import { Navigate, useBlocker, useParams } from 'react-router'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { errorMessage } from '@/api/client'
import { RestartTimeoutError, useRestart, useSettings } from '@/api/settings'
import { AdminHeader } from '@/components/AdminHeader'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Skeleton } from '@/components/ui/skeleton'
import { ConfigMenu } from '@/features/config/ConfigMenu'
import { GroupPage } from '@/features/config/GroupPage'
import { History } from '@/features/config/History'
import { Overview } from '@/features/config/Overview'
import {
  configPath,
  groupPages,
  historySlug,
  isChange,
  type ConfigPage,
  type Drafts,
} from '@/features/config/pages'
import { RestartBanner } from '@/features/config/RestartBanner'

export function AdminConfigPage() {
  const { data: me } = useMe()

  if (me?.role !== 'admin') {
    return <p className="p-6 text-sm text-muted-foreground">This page is for admins.</p>
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-8 p-6">
      <AdminHeader />
      <Config />
    </div>
  )
}

/**
 * The Config pages (D370, D391). Drafts are kept here, across them: each page saves its
 * own, the menu marks those with unsaved changes, and leaving Config asks first.
 */
function Config() {
  const { page: slug = '' } = useParams()
  const settings = useSettings()
  // Here rather than in the banner, which is gone by the time the server is back.
  const restart = useRestart()
  const [drafts, setDrafts] = useState<Drafts>({})
  const [version, setVersion] = useState(0)
  const [resets, setResets] = useState<Record<string, number>>({})

  const list = settings.data?.settings ?? []
  const unsavedOf = (keys: { key: string }[]) =>
    keys.filter((k) => {
      const s = list.find((x) => x.key === k.key)
      return s !== undefined && k.key in drafts && isChange(s, drafts[k.key])
    }).length
  const unsaved = unsavedOf(list)

  // Closing or reloading the tab with unsaved changes asks first; so does leaving Config
  // for another page of the app. Signing out is not held up.
  useEffect(() => {
    if (unsaved === 0) return

    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', warn)

    return () => window.removeEventListener('beforeunload', warn)
  }, [unsaved])

  const blocker = useBlocker(
    ({ nextLocation }) =>
      unsaved > 0 &&
      !nextLocation.pathname.startsWith(configPath()) &&
      nextLocation.pathname !== '/login',
  )

  if (settings.isPending) return <Skeleton className="h-96" />
  if (settings.isError) {
    return <ErrorAlert error={settings.error} onRetry={() => void settings.refetch()} />
  }

  const pages = groupPages(list)
  const page = pages.find((p) => p.slug === slug)
  if (slug !== '' && slug !== historySlug && !page) return <Navigate to={configPath()} replace />

  const byKey = new Map(list.map((s) => [s.key, s]))
  const unsavedIn = (p: ConfigPage) => unsavedOf(p.settings)

  const edit = (key: string, value: unknown) =>
    setDrafts((d) => {
      const next = { ...d }
      if (value === undefined) delete next[key]
      else next[key] = value
      return next
    })

  const reset = (key: string) => {
    edit(key, null)
    setResets((r) => ({ ...r, [key]: (r[key] ?? 0) + 1 }))
  }

  const clear = (keys: string[]) => {
    setDrafts((d) => Object.fromEntries(Object.entries(d).filter(([key]) => !keys.includes(key))))
    setVersion((v) => v + 1)
  }

  return (
    <div className="flex flex-col gap-6 md:flex-row md:items-start">
      <ConfigMenu pages={pages} current={slug} unsaved={unsavedIn} />

      <div className="flex min-w-0 flex-1 flex-col gap-8">
        {settings.data.restartPending && (
          <RestartBanner
            settings={list}
            restarting={restart.isPending}
            onRestart={() =>
              restart.mutate(undefined, {
                onSuccess: () => toast.success('Server restarted'),
                onError: (error) =>
                  toast.error(
                    error instanceof RestartTimeoutError ? error.message : errorMessage(error),
                  ),
              })
            }
          />
        )}

        {page ? (
          <GroupPage
            key={page.slug}
            page={page}
            byKey={byKey}
            drafts={drafts}
            version={version}
            resets={resets}
            onEdit={edit}
            onReset={reset}
            onClear={clear}
          />
        ) : slug === historySlug ? (
          <History settings={list} />
        ) : (
          <Overview pages={pages} unsaved={unsavedIn} />
        )}
      </div>

      <ConfirmDialog
        open={blocker.state === 'blocked'}
        title="Leave without saving?"
        description={`${unsaved} unsaved ${unsaved === 1 ? 'change' : 'changes'} on the Config pages will be lost.`}
        confirm="Leave"
        cancel="Stay"
        destructive
        onConfirm={() => blocker.proceed?.()}
        onCancel={() => blocker.reset?.()}
      />
    </div>
  )
}
