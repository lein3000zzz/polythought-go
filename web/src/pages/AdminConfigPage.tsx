import { Lock, RefreshCw, RotateCcw } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { ApiError, errorMessage, type ParamSpec } from '@/api/client'
import {
  RestartTimeoutError,
  useRestart,
  useSendTestMail,
  useSettingHistory,
  useSettings,
  useUpdateSettings,
  type Setting,
} from '@/api/settings'
import { AdminHeader } from '@/components/AdminHeader'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { ParamField } from '@/features/workflow/ParamField'
import { formatDateTime } from '@/lib/format'

export function AdminConfigPage() {
  const { data: me } = useMe()

  if (me?.role !== 'admin') {
    return <p className="p-6 text-sm text-muted-foreground">This page is for admins.</p>
  }

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-8 p-6">
      <AdminHeader />
      <Config />
    </div>
  )
}

/** New values by setting key. null resets a setting to its default. */
type Drafts = Record<string, unknown>

function Config() {
  const settings = useSettings()
  const update = useUpdateSettings()
  // Here rather than in the banner, which is gone by the time the server is back.
  const restart = useRestart()
  const [drafts, setDrafts] = useState<Drafts>({})
  // Bumped to remount fields, so they show saved values again instead of what was typed.
  const [version, setVersion] = useState(0)
  const [resets, setResets] = useState<Record<string, number>>({})

  if (settings.isPending) return <Skeleton className="h-96" />
  if (settings.isError) {
    return <ErrorAlert error={settings.error} onRetry={() => void settings.refetch()} />
  }

  const { settings: list, restartPending } = settings.data
  const byKey = new Map(list.map((s) => [s.key, s]))
  const changes = Object.fromEntries(
    Object.entries(drafts).filter(([key, value]) => {
      const s = byKey.get(key)
      return s !== undefined && isChange(s, value)
    }),
  )
  const count = Object.keys(changes).length

  const problems = new Map(
    update.error instanceof ApiError
      ? update.error.problems.flatMap((p) => (p.param ? [[p.param, p.message] as const] : []))
      : [],
  )

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

  const clear = () => {
    setDrafts({})
    setVersion((v) => v + 1)
    update.reset()
  }

  const save = () =>
    update.mutate(changes, {
      onSuccess: (data) => {
        clear()
        toast.success(
          data.restartPending ? 'Saved. Some changes apply after a restart.' : 'Settings saved',
        )
      },
    })

  return (
    <div className="flex flex-col gap-8">
      {restartPending && (
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

      {[...groups(list)].map(([group, items]) => (
        <section key={group} className="flex flex-col gap-5 rounded-lg border p-4">
          <h2 className="font-medium">{group}</h2>
          {items.map((s) => (
            <SettingRow
              key={`${s.key}-${version}-${resets[s.key] ?? 0}`}
              setting={s}
              draft={drafts[s.key]}
              hasDraft={s.key in drafts}
              problem={problems.get(s.key)}
              onChange={(value) => edit(s.key, value)}
              onReset={() => reset(s.key)}
            />
          ))}
          {group === mailGroup && <MailTest settings={byKey} />}
        </section>
      ))}

      {(count > 0 || update.isError) && (
        <div className="sticky bottom-0 -mx-6 flex flex-wrap items-center justify-end gap-3 border-t bg-background/95 px-6 py-3 backdrop-blur">
          <span className="mr-auto text-sm text-muted-foreground">
            {update.isError && problems.size === 0
              ? errorMessage(update.error)
              : `${count} unsaved ${count === 1 ? 'change' : 'changes'}`}
          </span>
          <Button variant="ghost" onClick={clear}>
            Discard
          </Button>
          <Button onClick={save} disabled={count === 0 || update.isPending}>
            Save
          </Button>
        </div>
      )}

      <History settings={list} />
    </div>
  )
}

const mailGroup = 'Mail'

/**
 * Sends a test email through the mail server as saved, not as edited (D168), and says what
 * the server answered when it did not take it.
 */
function MailTest({ settings }: { settings: Map<string, Setting> }) {
  const me = useMe()
  const send = useSendTestMail()
  const [to, setTo] = useState<string | null>(null)
  const [sentTo, setSentTo] = useState('')

  const address = to ?? me.data?.email ?? ''
  const setUp =
    Boolean(settings.get('mail.host')?.value) && Boolean(settings.get('mail.from_address')?.value)

  return (
    <form
      className="flex flex-col gap-2 border-t pt-4"
      onSubmit={(e) => {
        e.preventDefault()
        send.mutate(address, { onSuccess: () => setSentTo(address) })
      }}
    >
      <Label htmlFor="mail-test-to">Send a test email to</Label>
      <div className="flex flex-wrap gap-2">
        <Input
          id="mail-test-to"
          type="email"
          className="max-w-sm"
          value={address}
          onChange={(e) => setTo(e.target.value)}
          required
        />
        <Button type="submit" variant="outline" disabled={!setUp || send.isPending || !address}>
          {send.isPending ? 'Sending…' : 'Send test email'}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        {setUp
          ? 'Uses the saved settings: save changes first.'
          : 'Save a mail server and a From address first.'}
      </p>
      {send.isSuccess && sentTo && (
        <p className="text-sm">Sent to {sentTo}: look in its inbox, and its spam folder.</p>
      )}
      {send.isError && <ErrorAlert error={send.error} />}
    </form>
  )
}

/** Whether the environment or the config file sets it, which the page cannot change. */
function locked(s: Setting): boolean {
  return s.source === 'environment' || s.source === 'file'
}

/** Whether a draft value would change what is saved. */
function isChange(s: Setting, draft: unknown): boolean {
  if (locked(s)) return false
  if (draft === null) return s.secret ? s.isSet : s.source === 'database'

  return s.secret || JSON.stringify(draft) !== JSON.stringify(s.value)
}

function groups(list: Setting[]): Map<string, Setting[]> {
  const out = new Map<string, Setting[]>()
  for (const s of list) out.set(s.group, [...(out.get(s.group) ?? []), s])

  return out
}

const kinds: Record<Setting['kind'], ParamSpec['kind']> = {
  string: 'string',
  int: 'number',
  bool: 'bool',
  duration: 'duration',
  enum: 'string',
  list: 'list',
}

/** The setting as a param, so it gets the same field a node's setting would. */
function paramSpec(s: Setting): ParamSpec {
  const empty = Array.isArray(s.default) && s.default.length === 0

  return {
    name: s.key,
    label: s.label,
    kind: kinds[s.kind],
    options: s.options,
    default: empty ? undefined : s.default,
    description: s.description,
    required: false,
  }
}

type RowProps = {
  setting: Setting
  draft: unknown
  hasDraft: boolean
  problem?: string
  onChange: (value: unknown) => void // undefined drops the draft
  onReset: () => void
}

function SettingRow({ setting: s, draft, hasDraft, problem, onChange, onReset }: RowProps) {
  const isLocked = locked(s)
  // A setting at its default shows the default as a placeholder, so typing sets it.
  const value = hasDraft ? (draft ?? undefined) : s.source === 'default' ? undefined : s.value
  const resettable = !isLocked && draft !== null && (s.secret ? s.isSet : s.source === 'database')

  return (
    <div className="flex flex-col gap-2">
      <fieldset disabled={isLocked} className="min-w-0">
        {s.secret ? (
          <SecretField setting={s} draft={draft} problem={problem} onChange={onChange} />
        ) : (
          <ParamField
            spec={paramSpec(s)}
            scope="setting"
            value={value}
            problem={problem}
            // A cleared field means the default.
            onChange={(v) => onChange(v === undefined ? null : v)}
          />
        )}
      </fieldset>

      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        {isLocked ? (
          <Badge variant="secondary">
            <Lock />
            {s.source === 'file' ? 'Set in the config file' : `Set by ${s.env}`}
          </Badge>
        ) : (
          <Badge variant="outline">{s.source === 'database' ? 'Saved' : 'Default'}</Badge>
        )}
        {isLocked && (
          <span>
            {s.source === 'file'
              ? `Change ${s.key} in the server's config file.`
              : "Change it in the server's environment."}
          </span>
        )}
        {s.apply === 'restart' && !s.pending && <span>Applies after a restart.</span>}
        {s.pending && <Badge variant="outline">Waiting for a restart</Badge>}
        {hasDraft && draft === null && !isLocked && <span>Resets to the default when saved.</span>}
        {resettable && (
          <Button variant="ghost" size="xs" onClick={onReset}>
            <RotateCcw />
            {s.secret ? 'Clear' : 'Reset to default'}
          </Button>
        )}
      </div>
    </div>
  )
}

type SecretProps = {
  setting: Setting
  draft: unknown
  problem?: string
  onChange: (value: unknown) => void
}

/** A secret is never shown: it can only be replaced or cleared. */
function SecretField({ setting: s, draft, problem, onChange }: SecretProps) {
  const id = `setting-param-${s.key}`

  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{s.label}</Label>
      <Input
        id={id}
        type="password"
        autoComplete="off"
        value={typeof draft === 'string' ? draft : ''}
        placeholder={s.isSet ? 'Saved. Type a new value to replace it.' : 'Not set'}
        aria-invalid={Boolean(problem) || undefined}
        aria-describedby={`${id}-hint`}
        onChange={(e) => onChange(e.target.value === '' ? undefined : e.target.value)}
      />
      <div id={`${id}-hint`} className="flex flex-col gap-1 text-xs">
        <p className="text-muted-foreground">
          {s.description} Stored encrypted and never shown again.
        </p>
        {problem && (
          <p role="alert" className="text-destructive">
            {problem}
          </p>
        )}
      </div>
    </div>
  )
}

type BannerProps = {
  settings: Setting[]
  restarting: boolean
  onRestart: () => void
}

function RestartBanner({ settings, restarting, onRestart }: BannerProps) {
  const [asking, setAsking] = useState(false)
  const pending = settings.filter((s) => s.pending).map((s) => s.label)

  return (
    <Alert>
      <RefreshCw />
      <AlertTitle>A restart is needed</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
        <p>Changes to {pending.join(', ')} take effect when the server restarts.</p>
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

function History({ settings }: { settings: Setting[] }) {
  const history = useSettingHistory()
  const labels = new Map(settings.map((s) => [s.key, s.label]))
  const items = history.data?.pages.flatMap((page) => page.changes) ?? []

  return (
    <section className="flex flex-col gap-4">
      <h2 className="font-medium">History</h2>

      {history.isPending && <Skeleton className="h-24" />}
      {history.isError && (
        <ErrorAlert error={history.error} onRetry={() => void history.refetch()} />
      )}
      {history.isSuccess && items.length === 0 && (
        <p className="text-sm text-muted-foreground">No changes yet.</p>
      )}

      {items.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>When</TableHead>
              <TableHead>Setting</TableHead>
              <TableHead>Change</TableHead>
              <TableHead>By</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((c) => (
              <TableRow key={c.id}>
                <TableCell>{formatDateTime(c.at)}</TableCell>
                <TableCell>{labels.get(c.key) ?? c.key}</TableCell>
                <TableCell>
                  {c.secret ? (
                    'Replaced or cleared'
                  ) : (
                    <span className="flex flex-wrap items-center gap-1.5">
                      <Shown value={c.oldValue} /> → <Shown value={c.newValue} />
                    </span>
                  )}
                </TableCell>
                <TableCell>{c.actorEmail ?? 'A deleted account'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {history.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          onClick={() => void history.fetchNextPage()}
          disabled={history.isFetchingNextPage}
        >
          Load more
        </Button>
      )}
    </section>
  )
}

function Shown({ value }: { value: unknown }) {
  const text = Array.isArray(value) ? (value.length > 0 ? value.join(', ') : 'none') : String(value)

  return <code className="rounded bg-muted px-1 py-0.5 font-mono text-xs">{text}</code>
}
