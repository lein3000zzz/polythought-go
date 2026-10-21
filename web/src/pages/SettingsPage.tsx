import { CircleAlert, Copy, KeyRound, Send } from 'lucide-react'
import { Fragment, useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { useChangePassword, useCreateToken, useRevokeToken, useTokens } from '@/api/account'
import { useMe, useMinPasswordLength } from '@/api/auth'
import { ApiError, errorMessage, type Schemas } from '@/api/client'
import { useNotifications, useSetNotification } from '@/api/notifications'
import { tokens, useAgentSpend, useSetAgentChoices } from '@/api/spend'
import { useCreateTelegramLink, useTelegram, useUnlinkTelegram } from '@/api/telegram'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatDateTime } from '@/lib/format'

export function SettingsPage() {
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-10 p-6">
      <h1 className="font-heading text-xl font-semibold">Settings</h1>
      <PasswordSection />
      <TelegramSection />
      <NotificationsSection />
      <AgentsSection />
      <TokensSection />
    </div>
  )
}

type PasswordForm = { currentPassword: string; newPassword: string; repeat: string }

function PasswordSection() {
  const minPasswordLength = useMinPasswordLength()
  const change = useChangePassword()
  const { register, handleSubmit, reset, formState, setError } = useForm<PasswordForm>()
  const wrongPassword =
    change.error instanceof ApiError && change.error.code === 'invalid_credentials'

  return (
    <section className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">Password</h2>
        <p className="text-sm text-muted-foreground">Changing it signs you out everywhere else.</p>
      </div>

      <form
        className="flex max-w-sm flex-col gap-4"
        onSubmit={handleSubmit(({ currentPassword, newPassword, repeat }) => {
          if (newPassword !== repeat) {
            setError('repeat', { message: 'The new passwords differ' })
            return
          }

          change.mutate(
            { currentPassword, newPassword },
            {
              onSuccess: () => {
                reset()
                toast.success('Password changed')
              },
            },
          )
        })}
      >
        {change.isError && !wrongPassword && <ErrorAlert error={change.error} />}

        <div className="flex flex-col gap-2">
          <Label htmlFor="current-password">Current password</Label>
          <Input
            id="current-password"
            type="password"
            autoComplete="current-password"
            required
            aria-invalid={wrongPassword || undefined}
            {...register('currentPassword')}
          />
          {wrongPassword && (
            <p role="alert" className="text-xs text-destructive">
              That is not your current password.
            </p>
          )}
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="new-password">New password</Label>
          <Input
            id="new-password"
            type="password"
            autoComplete="new-password"
            minLength={minPasswordLength}
            required
            aria-describedby="new-password-hint"
            {...register('newPassword')}
          />
          <p id="new-password-hint" className="text-xs text-muted-foreground">
            {minPasswordLength && `At least ${minPasswordLength} characters.`}
          </p>
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="repeat-password">Repeat the new password</Label>
          <Input
            id="repeat-password"
            type="password"
            autoComplete="new-password"
            required
            aria-invalid={Boolean(formState.errors.repeat) || undefined}
            {...register('repeat')}
          />
          {formState.errors.repeat && (
            <p role="alert" className="text-xs text-destructive">
              {formState.errors.repeat.message}
            </p>
          )}
        </div>

        <Button type="submit" className="self-start" disabled={change.isPending}>
          Change password
        </Button>
      </form>
    </section>
  )
}

type TokenForm = { name: string; expires: string }

function TokensSection() {
  const tokens = useTokens()
  const create = useCreateToken()
  const [created, setCreated] = useState<Schemas['APIToken']>()
  const [revoking, setRevoking] = useState<Schemas['APIToken']>()
  const revoke = useRevokeToken()
  const { register, handleSubmit, reset } = useForm<TokenForm>()
  // The date picker's first day, in the user's time zone (en-CA writes YYYY-MM-DD).
  const [today] = useState(() => new Date().toLocaleDateString('en-CA'))

  return (
    <section className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">API tokens</h2>
        <p className="text-sm text-muted-foreground">
          For scripts and tools: send{' '}
          <code className="font-mono text-xs">Authorization: Bearer pt_…</code> with requests to{' '}
          <code className="font-mono text-xs">/api/v1</code>. A token can do what you can, except
          change your password, create or revoke tokens, and store wallet credentials.
        </p>
      </div>

      {created?.token && (
        <Alert>
          <KeyRound />
          <AlertTitle>Copy the token “{created.name}” now</AlertTitle>
          <AlertDescription className="flex flex-col gap-2">
            <p>It is shown only this once.</p>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 font-mono text-xs">
                {created.token}
              </code>
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  void navigator.clipboard
                    .writeText(created.token ?? '')
                    .then(() => toast.success('Token copied'))
                }
              >
                <Copy />
                Copy
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setCreated(undefined)}>
                Done
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      )}

      <form
        className="flex flex-wrap items-end gap-3 rounded-lg border p-4"
        onSubmit={handleSubmit(({ name, expires }) =>
          create.mutate(
            {
              name,
              // The end of the chosen day, in the user's time zone.
              ...(expires ? { expiresAt: new Date(`${expires}T23:59:59`).toISOString() } : {}),
            },
            {
              onSuccess: (token) => {
                reset()
                setCreated(token)
              },
            },
          ),
        )}
      >
        {create.isError && (
          <div className="w-full">
            <ErrorAlert error={create.error} />
          </div>
        )}
        <div className="flex min-w-56 flex-1 flex-col gap-2">
          <Label htmlFor="token-name">Name</Label>
          <Input
            id="token-name"
            placeholder="e.g. backtest script"
            required
            {...register('name')}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="token-expires">Expires</Label>
          <Input id="token-expires" type="date" min={today} {...register('expires')} />
        </div>
        <Button type="submit" disabled={create.isPending}>
          Create token
        </Button>
      </form>

      {tokens.isPending && <Skeleton className="h-24" />}
      {tokens.isError && <ErrorAlert error={tokens.error} onRetry={() => void tokens.refetch()} />}
      {tokens.data?.length === 0 && <p className="text-sm text-muted-foreground">No tokens yet.</p>}

      {tokens.data && tokens.data.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Token</TableHead>
              <TableHead>Created</TableHead>
              <TableHead>Last used</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokens.data.map((t) => (
              <TableRow key={t.id}>
                <TableCell className="font-medium">{t.name}</TableCell>
                <TableCell className="font-mono text-xs">{t.prefix}…</TableCell>
                <TableCell>{formatDateTime(t.createdAt)}</TableCell>
                <TableCell>{t.lastUsedAt ? formatDateTime(t.lastUsedAt) : 'Never'}</TableCell>
                <TableCell>{t.expiresAt ? formatDateTime(t.expiresAt) : 'Never'}</TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-destructive"
                    onClick={() => setRevoking(t)}
                  >
                    Revoke
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <ConfirmDialog
        open={revoking !== undefined}
        title={`Revoke “${revoking?.name}”?`}
        description="Anything using it stops working at once."
        confirm="Revoke"
        destructive
        onCancel={() => setRevoking(undefined)}
        onConfirm={() =>
          revoking &&
          revoke.mutate(revoking.id, {
            onSuccess: () => {
              setRevoking(undefined)
              toast.success('Token revoked')
            },
            onError: (error) => toast.error(errorMessage(error)),
          })
        }
      />
    </section>
  )
}

function TelegramSection() {
  const { data: me } = useMe()
  const [link, setLink] = useState<Schemas['TelegramLinkURL']>()
  const telegram = useTelegram(link?.expiresAt)
  const create = useCreateTelegramLink()
  const unlink = useUnlinkTelegram()
  const [confirmUnlink, setConfirmUnlink] = useState(false)

  const linked = telegram.data?.link
  const admin = me?.role === 'admin'

  return (
    <section className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">Telegram</h2>
        <p className="text-sm text-muted-foreground">
          Link Telegram to get messages from Polythought there.
        </p>
      </div>

      {telegram.isPending && <Skeleton className="h-12" />}
      {telegram.isError && (
        <ErrorAlert error={telegram.error} onRetry={() => void telegram.refetch()} />
      )}

      {telegram.data && !telegram.data.available && !linked && (
        <p className="text-sm text-muted-foreground">
          Telegram is not set up on this instance.
          {admin && (
            <>
              {' '}
              Turn on the bot and add its token under{' '}
              <Link to="/admin/config" className="text-foreground underline underline-offset-4">
                Admin → Config
              </Link>
              .
            </>
          )}
        </p>
      )}

      {admin && telegram.data?.problem && (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertTitle>The bot reported a problem</AlertTitle>
          <AlertDescription>
            {telegram.data.problem}
            {telegram.data.problemAt && ` (${formatDateTime(telegram.data.problemAt)})`}
          </AlertDescription>
        </Alert>
      )}

      {linked && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4">
          <div className="flex items-center gap-2 text-sm">
            <Send className="size-4 text-muted-foreground" />
            <span>
              Linked to{' '}
              <span className="font-medium">
                {linked.username ? `@${linked.username}` : linked.firstName || 'your Telegram'}
              </span>{' '}
              <span className="text-muted-foreground">since {formatDateTime(linked.linkedAt)}</span>
            </span>
          </div>
          <Button variant="outline" size="sm" onClick={() => setConfirmUnlink(true)}>
            Unlink
          </Button>
        </div>
      )}

      {telegram.data?.available && !linked && !link && (
        <div className="flex flex-col items-start gap-2">
          {create.isError && <ErrorAlert error={create.error} />}
          <Button
            variant="outline"
            disabled={create.isPending}
            onClick={() => create.mutate(undefined, { onSuccess: setLink })}
          >
            <Send />
            Link Telegram
          </Button>
        </div>
      )}

      {telegram.data?.available && !linked && link && (
        <Alert>
          <Send />
          <AlertTitle>Open the link in Telegram and press Start</AlertTitle>
          <AlertDescription className="flex flex-col gap-2">
            <p>
              It links the Telegram account that opens it, once, until{' '}
              {formatDateTime(link.expiresAt)}. This page notices when it is done.
            </p>
            <div className="flex gap-2">
              <Button size="sm" asChild>
                <a href={link.url} target="_blank" rel="noopener">
                  Open Telegram
                </a>
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setLink(undefined)}>
                Cancel
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      )}

      <ConfirmDialog
        open={confirmUnlink}
        title="Unlink Telegram?"
        description="Polythought stops writing to that chat. You can link it again any time."
        confirm="Unlink"
        destructive
        onCancel={() => setConfirmUnlink(false)}
        onConfirm={() => {
          setConfirmUnlink(false)
          setLink(undefined)
          unlink.mutate(undefined, {
            onSuccess: () => toast.success('Telegram unlinked'),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }}
      />
    </section>
  )
}

function NotificationsSection() {
  const notifications = useNotifications()
  const telegram = useTelegram()
  const me = useMe()
  const set = useSetNotification()
  const mailOn = notifications.data?.mailOn

  const change = (key: string, body: { enabled?: boolean; email?: boolean }) =>
    set.mutate({ key, ...body }, { onError: (error) => toast.error(errorMessage(error)) })

  return (
    <section className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">Notifications</h2>
        <p className="text-sm text-muted-foreground">
          On Telegram, to your linked chat
          {telegram.data && !telegram.data.link && ' (link Telegram above to get them)'}; by email,
          to {me.data?.email ?? 'your address'}.
          {notifications.data &&
            !mailOn &&
            ' Email needs an admin to allow it, and mail set up on the server.'}
        </p>
      </div>

      {notifications.isPending && <Skeleton className="h-12" />}
      {notifications.isError && (
        <ErrorAlert error={notifications.error} onRetry={() => void notifications.refetch()} />
      )}

      {notifications.data && notifications.data.notifications.length > 0 && (
        <div className="flex justify-end gap-6 text-xs text-muted-foreground" aria-hidden>
          <span className="w-9 text-center">Telegram</span>
          <span className="w-9 text-center">Email</span>
        </div>
      )}

      {notifications.data?.notifications.map((n, i, all) => (
        <Fragment key={n.key}>
          {/* The kinds come grouped: a heading where a group starts. */}
          {n.group !== all[i - 1]?.group && (
            <h3 className="pt-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">
              {n.group}
            </h3>
          )}
          <div className="flex items-start justify-between gap-4">
            <div className="flex flex-col gap-0.5">
              <span className="text-sm font-medium">{n.label}</span>
              <p id={`notify-${n.key}-hint`} className="text-xs text-muted-foreground">
                {n.description}
              </p>
            </div>
            <div className="flex shrink-0 gap-6">
              <Switch
                aria-label={`${n.label}, on Telegram`}
                aria-describedby={`notify-${n.key}-hint`}
                checked={n.enabled}
                disabled={set.isPending}
                onCheckedChange={(enabled) => change(n.key, { enabled })}
              />
              <Switch
                aria-label={`${n.label}, by email`}
                aria-describedby={`notify-${n.key}-hint`}
                checked={n.email}
                disabled={set.isPending || !mailOn}
                onCheckedChange={(email) => change(n.key, { email })}
              />
            </div>
          </div>
        </Fragment>
      ))}
    </section>
  )
}

/**
 * What your AI agent nodes spent this month with your own keys, the caps on it (D277),
 * and whether your kill switch stops them too (D276). Not there without agents.
 */
function AgentsSection() {
  const spend = useAgentSpend()
  const choose = useSetAgentChoices()

  if (spend.error instanceof ApiError && spend.error.status === 404) return null

  const d = spend.data
  const tokenCap = (cap: number | undefined) => (cap ? `of ${tokens(cap)}` : 'no cap')

  return (
    <section className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">AI agents</h2>
        <p className="text-sm text-muted-foreground">
          What your AI agent nodes spent this month with your own keys. Dollars are what providers
          report: OpenRouter does, the others do not, so their calls count in tokens only. An admin
          sets the caps; a call that would pass one is refused, and its item fails.
        </p>
      </div>

      {spend.isPending && <Skeleton className="h-16" />}
      {spend.isError && <ErrorAlert error={spend.error} onRetry={() => void spend.refetch()} />}

      {d && (
        <>
          <dl
            className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm"
            aria-label="Agent spend"
          >
            <dt className="text-muted-foreground">Dollars</dt>
            <dd>
              ${d.dollars}{' '}
              {d.caps.dollars && Number(d.caps.dollars) > 0 ? `of $${d.caps.dollars}` : 'no cap'}
              <span className="text-xs text-muted-foreground">
                {' '}
                ({d.costedCalls} of {d.calls} calls priced by their provider)
              </span>
            </dd>
            <dt className="text-muted-foreground">Tokens</dt>
            <dd>
              {tokens(d.totalTokens)} {tokenCap(d.caps.totalTokens)}
            </dd>
            <dt className="text-muted-foreground">Input tokens</dt>
            <dd>
              {tokens(d.inputTokens)} {tokenCap(d.caps.inputTokens)}
            </dd>
            <dt className="text-muted-foreground">Output tokens</dt>
            <dd>
              {tokens(d.outputTokens)} {tokenCap(d.caps.outputTokens)}
            </dd>
          </dl>
          <p className="text-xs text-muted-foreground">
            The month is UTC&apos;s: it starts again {formatDateTime(d.resets)}.
          </p>

          <div className="flex items-start justify-between gap-4">
            <div className="flex flex-col gap-0.5">
              <Label htmlFor="kill-stops-agents">My kill switch stops my agents too</Label>
              <p id="kill-stops-agents-hint" className="text-xs text-muted-foreground">
                While your live trading is stopped, your AI agent nodes make no calls either: their
                items fail. Off, the kill switch stops live trading only.
              </p>
            </div>
            <Switch
              id="kill-stops-agents"
              aria-describedby="kill-stops-agents-hint"
              checked={d.killStopsAgents}
              disabled={choose.isPending}
              onCheckedChange={(on) =>
                choose.mutate(
                  { killStopsAgents: on },
                  {
                    onSuccess: () =>
                      toast.success(
                        on
                          ? 'Your kill switch stops your agents too'
                          : 'Your kill switch stops live trading only',
                      ),
                    onError: (error) => toast.error(errorMessage(error)),
                  },
                )
              }
            />
          </div>
        </>
      )}
    </section>
  )
}
