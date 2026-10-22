import { RefreshCw, Upload } from 'lucide-react'
import { useRef, useState } from 'react'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { errorMessage } from '@/api/client'
import {
  type FoundPlugin,
  type InstalledPlugin,
  type Plugin,
  PluginRestartTimeoutError,
  useAdoptPlugin,
  useFetchPlugin,
  useInstallPlugin,
  usePluginHistory,
  usePlugins,
  useRemovePluginVersion,
  useRestartForPlugins,
  useSetPluginEnabled,
  useStartPlugin,
  useUninstallPlugin,
} from '@/api/plugins'
import { AdminHeader } from '@/components/AdminHeader'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { ApproveDialog } from '@/features/plugins/ApproveDialog'
import { signedBy } from '@/features/plugins/signer'
import { UsersDialog } from '@/features/plugins/UsersDialog'
import { formatDateTime } from '@/lib/format'

export function AdminPluginsPage() {
  const { data: me } = useMe()

  if (me?.role !== 'admin') {
    return <p className="p-6 text-sm text-muted-foreground">This page is for admins.</p>
  }

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-10 p-6">
      <AdminHeader />
      <PluginsSection />
    </div>
  )
}

type Approving = { id: string; version: string }

function PluginsSection() {
  const plugins = usePlugins()
  const restart = useRestartForPlugins()
  const [approving, setApproving] = useState<Approving>()
  const [choosing, setChoosing] = useState<Plugin>()

  if (plugins.isPending) return <Skeleton className="h-40" />
  if (plugins.isError) {
    return <ErrorAlert error={plugins.error} onRetry={() => void plugins.refetch()} />
  }

  const { plugins: list, found } = plugins.data
  const approvingPlugin = approving && list.find((p) => p.id === approving.id)
  const review = (installed: Pick<InstalledPlugin, 'id' | 'version'>) =>
    setApproving({ id: installed.id, version: installed.version })

  return (
    <div className="flex flex-col gap-8">
      {list.some((p) => p.restart) && (
        <RestartBanner
          plugins={list}
          restarting={restart.isPending}
          onRestart={() =>
            restart.mutate(undefined, {
              onSuccess: () => toast.success('Server restarted'),
              onError: (error) =>
                toast.error(
                  error instanceof PluginRestartTimeoutError ? error.message : errorMessage(error),
                ),
            })
          }
        />
      )}

      <InstallSection onInstalled={review} />

      <section className="flex flex-col gap-4">
        <h2 className="font-medium">Installed</h2>
        {list.length === 0 && (
          <p className="text-sm text-muted-foreground">No plugin is installed yet.</p>
        )}
        {list.map((p) => (
          <PluginCard
            key={p.id}
            plugin={p}
            onReview={(version) => setApproving({ id: p.id, version })}
            onChooseUsers={() => setChoosing(p)}
          />
        ))}
      </section>

      {found.length > 0 && <FoundSection found={found} onAdopted={review} />}

      {approvingPlugin && approving && (
        <ApproveDialog
          plugin={approvingPlugin}
          version={approving.version}
          onClose={() => setApproving(undefined)}
        />
      )}

      {choosing && <UsersDialog plugin={choosing} onClose={() => setChoosing(undefined)} />}
    </div>
  )
}

type BannerProps = {
  plugins: Plugin[]
  restarting: boolean
  onRestart: () => void
}

/** What waits for a restart (D317), with the restart the Config page has too. */
function RestartBanner({ plugins, restarting, onRestart }: BannerProps) {
  const [asking, setAsking] = useState(false)
  const pending = plugins
    .filter((p) => p.restart)
    .map((p) => (p.enabled && p.version ? `${p.name} ${p.version} runs` : `${p.name} stops`))

  return (
    <Alert>
      <RefreshCw />
      <AlertTitle>A restart is needed</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
        <p>Once the server restarts: {pending.join('; ')}.</p>
        <Button size="sm" onClick={() => setAsking(true)} disabled={restarting}>
          {restarting ? 'Restarting…' : 'Restart now'}
        </Button>
      </AlertDescription>

      <ConfirmDialog
        open={asking}
        title="Restart the server?"
        description="It stops and starts again, which takes a few seconds. Active workflows stop and start again, runs in progress are cut short, and open pages reconnect."
        confirm="Restart"
        onCancel={() => setAsking(false)}
        onConfirm={() => {
          setAsking(false)
          onRestart()
        }}
      />
    </Alert>
  )
}

/** Install from an archive the admin picks, or from an https URL; approving comes next. */
function InstallSection({ onInstalled }: { onInstalled: (i: InstalledPlugin) => void }) {
  const install = useInstallPlugin()
  const fetchFrom = useFetchPlugin()
  const file = useRef<HTMLInputElement>(null)
  const [picked, setPicked] = useState<File>()
  const [url, setUrl] = useState('')

  const done = (installed: InstalledPlugin) => {
    toast.success(`${installed.name} ${installed.version} is installed: review what it asks for`)
    setPicked(undefined)
    setUrl('')
    if (file.current) file.current.value = ''
    onInstalled(installed)
  }

  const error = install.error ?? fetchFrom.error

  return (
    <section className="flex flex-col gap-4 rounded-lg border p-4">
      <h2 className="font-medium">Install a plugin</h2>
      <p className="text-sm text-muted-foreground">
        From its archive, a .tar.gz. Nothing of it runs until you approve what it asks for and the
        server restarts.
      </p>

      <div className="flex flex-wrap items-end gap-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="plugin-archive">Archive</Label>
          <Input
            id="plugin-archive"
            ref={file}
            type="file"
            accept=".tar.gz,.tgz,application/gzip"
            onChange={(e) => setPicked(e.target.files?.[0])}
          />
        </div>
        <Button
          onClick={() => picked && install.mutate(picked, { onSuccess: done })}
          disabled={!picked || install.isPending}
        >
          <Upload />
          {install.isPending ? 'Installing…' : 'Install'}
        </Button>
      </div>

      <div className="flex flex-wrap items-end gap-2">
        <div className="flex min-w-72 flex-1 flex-col gap-1.5">
          <Label htmlFor="plugin-url">Or from a URL</Label>
          <Input
            id="plugin-url"
            type="url"
            placeholder="https://example.com/weather-1.0.0.tar.gz"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
        </div>
        <Button
          variant="outline"
          onClick={() => fetchFrom.mutate(url.trim(), { onSuccess: done })}
          disabled={url.trim() === '' || fetchFrom.isPending}
        >
          {fetchFrom.isPending ? 'Fetching…' : 'Fetch and install'}
        </Button>
      </div>

      {error && <ErrorAlert error={error} />}
    </section>
  )
}

/** How a plugin runs, in a word. */
function stateOf(p: Plugin): { label: string; bad: boolean } {
  switch (p.status?.state) {
    case 'running':
      return { label: 'Running', bad: false }
    case 'restarting':
      return { label: 'Restarting after a crash', bad: true }
    case 'crashed':
      return { label: 'Crashed', bad: true }
    case 'failed':
      return { label: 'Would not start', bad: true }
    case 'stopped':
      return { label: 'Stopped', bad: false }
  }

  if (!p.version) return { label: 'Waiting for approval', bad: false }
  if (!p.enabled) return { label: 'Disabled', bad: false }

  return { label: 'Not running', bad: false }
}

type CardProps = {
  plugin: Plugin
  onReview: (version: string) => void
  onChooseUsers: () => void
}

function PluginCard({ plugin: p, onReview, onChooseUsers }: CardProps) {
  const setEnabled = useSetPluginEnabled()
  const start = useStartPlugin()
  const uninstall = useUninstallPlugin()
  const removeVersion = useRemovePluginVersion()
  const [uninstalling, setUninstalling] = useState(false)
  const [history, setHistory] = useState(false)

  const state = stateOf(p)
  const runs = p.versions.find((v) => v.version === p.version)
  const waiting = p.versions.filter((v) => !v.approved)
  const fail = (error: unknown) => toast.error(errorMessage(error))

  return (
    <article aria-label={p.name} className="flex flex-col gap-4 rounded-lg border p-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h3 className="font-medium">
            {p.name} <span className="text-sm font-normal text-muted-foreground">{p.id}</span>
          </h3>
          <p className="text-sm text-muted-foreground">
            {runs ? `Version ${runs.version}. ${signedBy(runs).text}.` : 'No version approved yet.'}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={state.bad ? 'destructive' : 'secondary'}>{state.label}</Badge>
          {p.restart && <Badge variant="outline">Waits for a restart</Badge>}
        </div>
      </header>

      {p.status && (p.status.lastError || p.status.crashes > 0) && (
        <div className="flex flex-col gap-1 text-sm">
          {p.status.crashes > 0 && (
            <p>
              {p.status.crashes} {p.status.crashes === 1 ? 'crash' : 'crashes'} in the last 10
              minutes, since {formatDateTime(p.status.since)}.
            </p>
          )}
          {p.status.lastError && <p className="text-destructive">{p.status.lastError}</p>}
        </div>
      )}

      {p.status && p.status.log.length > 0 && (
        <details className="text-sm">
          <summary className="cursor-pointer text-muted-foreground">Its log</summary>
          <pre className="mt-2 max-h-64 overflow-auto rounded-md bg-muted p-3 text-xs whitespace-pre-wrap">
            {p.status.log.join('\n')}
          </pre>
        </details>
      )}

      {waiting.map((v) => (
        <div
          key={v.version}
          className="flex flex-wrap items-center justify-between gap-2 rounded-md bg-muted/50 p-3 text-sm"
        >
          <span>
            Version {v.version} waits for your approval. {signedBy(v).text}.
          </span>
          <span className="flex gap-2">
            <Button size="sm" onClick={() => onReview(v.version)}>
              Review
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() =>
                removeVersion.mutate(
                  { id: p.id, version: v.version },
                  {
                    onSuccess: () => toast.success(`${p.name} ${v.version} is removed`),
                    onError: fail,
                  },
                )
              }
            >
              Remove
            </Button>
          </span>
        </div>
      ))}

      <footer className="flex flex-wrap items-center gap-x-6 gap-y-3 text-sm">
        <label className="flex items-center gap-2">
          <Switch
            checked={p.enabled}
            disabled={setEnabled.isPending}
            onCheckedChange={(enabled) =>
              setEnabled.mutate(
                { id: p.id, enabled },
                {
                  onSuccess: () =>
                    toast.success(
                      enabled
                        ? `${p.name} is enabled: it runs once the server restarts`
                        : `${p.name} is disabled: it stops once the server restarts, and the workflows using it are paused`,
                    ),
                  onError: fail,
                },
              )
            }
          />
          Enabled
        </label>

        <span className="flex items-center gap-2">
          {p.everyone ? 'Everyone may use it' : `${p.users.length} chosen users may use it`}
          <Button size="sm" variant="outline" onClick={onChooseUsers}>
            Who may use it
          </Button>
        </span>

        {p.status?.state === 'crashed' && (
          <Button
            size="sm"
            onClick={() =>
              start.mutate(p.id, {
                onSuccess: () => toast.success(`${p.name} is starting`),
                onError: fail,
              })
            }
            disabled={start.isPending}
          >
            Start again
          </Button>
        )}

        <Button size="sm" variant="ghost" onClick={() => setHistory(true)}>
          History
        </Button>
        <Button
          size="sm"
          variant="ghost"
          className="text-destructive"
          onClick={() => setUninstalling(true)}
        >
          Uninstall
        </Button>
      </footer>

      <ConfirmDialog
        open={uninstalling}
        title={`Uninstall ${p.name}?`}
        description="Its files are removed now. It runs until the server restarts; then the active workflows using it are paused, and their owners told. What admins did with it is kept."
        confirm="Uninstall"
        destructive
        onCancel={() => setUninstalling(false)}
        onConfirm={() => {
          setUninstalling(false)
          uninstall.mutate(p.id, {
            onSuccess: () => toast.success(`${p.name} is uninstalled`),
            onError: fail,
          })
        }}
      />

      {history && <HistoryDialog plugin={p} onClose={() => setHistory(false)} />}
    </article>
  )
}

function HistoryDialog({ plugin, onClose }: { plugin: Plugin; onClose: () => void }) {
  const history = usePluginHistory(plugin.id, true)

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{plugin.name}: what admins did</DialogTitle>
          <DialogDescription>Newest first.</DialogDescription>
        </DialogHeader>
        {history.isPending && <Skeleton className="h-24" />}
        {history.isError && <ErrorAlert error={history.error} />}
        <ul className="flex flex-col gap-2 text-sm">
          {history.data?.map((a, i) => (
            <li key={i} className="flex flex-col">
              <span>
                {a.action}
                {a.version && ` ${a.version}`}
                {a.detail && <span className="text-muted-foreground">: {a.detail}</span>}
              </span>
              <span className="text-xs text-muted-foreground">
                {formatDateTime(a.at)}
                {a.by ? '' : ', by the server'}
              </span>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  )
}

/** Versions on disk no admin installed (D321): installed as an archive would be. */
function FoundSection({
  found,
  onAdopted,
}: {
  found: FoundPlugin[]
  onAdopted: (i: InstalledPlugin) => void
}) {
  const adopt = useAdoptPlugin()

  return (
    <section className="flex flex-col gap-3">
      <h2 className="font-medium">Found in the plugins directory</h2>
      <p className="text-sm text-muted-foreground">
        Someone put these there, not through this page. Install one to check it and review what it
        asks for.
      </p>
      <ul className="flex flex-col gap-2">
        {found.map((f) => (
          <li
            key={`${f.id}@${f.version}`}
            className="flex items-center justify-between gap-2 text-sm"
          >
            <span>
              {f.id} {f.version}
            </span>
            <Button
              size="sm"
              variant="outline"
              disabled={adopt.isPending}
              onClick={() =>
                adopt.mutate(f, {
                  onSuccess: onAdopted,
                  onError: (error) => toast.error(errorMessage(error)),
                })
              }
            >
              Install
            </Button>
          </li>
        ))}
      </ul>
    </section>
  )
}
