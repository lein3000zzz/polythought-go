import { ChevronDown, ChevronRight, Copy, Plus } from 'lucide-react'
import { Fragment, useState } from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import {
  useApproveJoin,
  useCreateInvite,
  useDenyJoin,
  useInviteCodeCheck,
  useInvites,
  useJoinRequests,
  useRevokeInvite,
} from '@/api/admin'
import { useAuthConfig, useMe } from '@/api/auth'
import { errorMessage, type Schemas } from '@/api/client'
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
import { configPath } from '@/features/config/pages'
import { useDebounced } from '@/lib/debounce'
import { formatDateTime } from '@/lib/format'

type Invite = Schemas['Invite']

const registrationModes: Record<Schemas['AuthConfig']['registration'], string> = {
  open: 'Anyone can create an account.',
  invite: 'People need an invite to create an account.',
  closed: 'Only admins create accounts.',
}

const statuses: Record<
  Invite['status'],
  { label: string; variant: 'default' | 'secondary' | 'outline' }
> = {
  open: { label: 'Open', variant: 'default' },
  used: { label: 'Used up', variant: 'secondary' },
  expired: { label: 'Expired', variant: 'outline' },
  revoked: { label: 'Revoked', variant: 'outline' },
}

function inviteLink(code: string): string {
  return `${window.location.origin}/register?invite=${encodeURIComponent(code)}`
}

function copyLink(code: string) {
  void navigator.clipboard
    .writeText(inviteLink(code))
    .then(() => toast.success('Invite link copied'))
}

/** Admin → Invites (D371, D393): codes to share, as many times as each admits. */
export function AdminInvitesPage() {
  const { data: me } = useMe()

  if (me?.role !== 'admin') {
    return <p className="p-6 text-sm text-muted-foreground">This page is for admins.</p>
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-8 p-6">
      <AdminHeader />
      <Invites />
    </div>
  )
}

function Invites() {
  const config = useAuthConfig()
  const invites = useInvites()
  const [created, setCreated] = useState<Invite>()
  const list = invites.data?.invites
  const open = list?.filter((i) => i.status === 'open').length ?? 0
  const signups = list?.reduce((n, i) => n + i.used, 0) ?? 0

  return (
    <div className="flex flex-col gap-8">
      <Requests onApproved={setCreated} />

      <section className="flex flex-col gap-4">
        <div>
          <h2 className="font-medium">Invites</h2>
          {config.data && (
            <p className="text-sm text-muted-foreground">
              {registrationModes[config.data.registration]} Change who may sign up under{' '}
              <Link
                to={configPath('accounts', 'auth.registration')}
                className="text-foreground underline underline-offset-4"
              >
                Config
              </Link>
              .
            </p>
          )}
        </div>

        <CreateForm onCreated={setCreated} />
        {invites.data?.byEmail && <MailForm onCreated={setCreated} />}

        {created?.code && (
          <Alert>
            <AlertTitle>
              {!created.email
                ? 'Invite made: share its link'
                : created.sent
                  ? `Mailed to ${created.email}`
                  : `The mail to ${created.email} did not go out: send this link yourself`}
            </AlertTitle>
            <AlertDescription className="flex flex-col gap-2">
              <p>
                It admits {created.maxUses === 1 ? 'one sign-up' : `${created.maxUses} sign-ups`}{' '}
                until {formatDateTime(created.expiresAt)}
                {created.email && `, ${created.email} alone`}.
              </p>
              <div className="flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 font-mono text-xs">
                  {inviteLink(created.code)}
                </code>
                <Button variant="outline" size="sm" onClick={() => copyLink(created.code!)}>
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
      </section>

      <section className="flex flex-col gap-4">
        <div className="flex items-baseline justify-between gap-4">
          <h2 className="font-medium">All invites</h2>
          {list && list.length > 0 && (
            <p className="text-sm text-muted-foreground">
              {open} open · {signups} {signups === 1 ? 'sign-up' : 'sign-ups'} through invites
            </p>
          )}
        </div>

        {invites.isPending && <Skeleton className="h-24" />}
        {invites.isError && (
          <ErrorAlert error={invites.error} onRetry={() => void invites.refetch()} />
        )}
        {list?.length === 0 && <p className="text-sm text-muted-foreground">No invites yet.</p>}
        {list && list.length > 0 && <InviteTable invites={list} />}
      </section>
    </div>
  )
}

/** A code typed, checked as it is, or made; its uses; its end (D371). */
function CreateForm({ onCreated }: { onCreated: (invite: Invite) => void }) {
  const create = useCreateInvite()
  const [code, setCode] = useState('')
  const [uses, setUses] = useState('1')
  const [expires, setExpires] = useState('')
  // The date picker's bounds, in the admin's time zone (en-CA writes YYYY-MM-DD).
  const [[today, inAYear]] = useState(() => {
    const now = new Date()
    const later = new Date(now)
    later.setFullYear(now.getFullYear() + 1)
    later.setDate(later.getDate() - 1)

    return [now.toLocaleDateString('en-CA'), later.toLocaleDateString('en-CA')]
  })

  const typed = useDebounced(code.trim(), 300)
  const check = useInviteCodeCheck(typed)
  const settled = typed === code.trim()
  const problem = settled ? check.data?.problem : undefined
  const free = settled && typed !== '' && check.isSuccess && !check.data.problem

  return (
    <form
      className="flex flex-wrap items-start gap-3 rounded-lg border p-4"
      onSubmit={(e) => {
        e.preventDefault()
        create.mutate(
          {
            ...(code.trim() ? { code: code.trim() } : {}),
            maxUses: Number(uses) || 1,
            ...(expires ? { expiresAt: new Date(`${expires}T23:59:59`).toISOString() } : {}),
          },
          {
            onSuccess: (invite) => {
              onCreated(invite)
              setCode('')
              setUses('1')
              setExpires('')
            },
            onError: (error) => toast.error(errorMessage(error)),
          },
        )
      }}
    >
      <div className="flex min-w-64 flex-1 flex-col gap-2">
        <Label htmlFor="invite-code">Code</Label>
        <Input
          id="invite-code"
          className="font-mono"
          placeholder="Leave empty to have one made"
          autoComplete="off"
          spellCheck={false}
          maxLength={64}
          value={code}
          aria-invalid={Boolean(problem) || undefined}
          aria-describedby="invite-code-hint"
          onChange={(e) => setCode(e.target.value)}
        />
        <p
          id="invite-code-hint"
          role={problem ? 'alert' : undefined}
          className={problem ? 'text-xs text-destructive' : 'text-xs text-muted-foreground'}
        >
          {problem ??
            (free
              ? 'Free to take.'
              : '8 to 64 letters, digits, - and _; any case is the same code.')}
        </p>
      </div>
      <div className="flex w-28 flex-col gap-2">
        <Label htmlFor="invite-uses">Sign-ups</Label>
        <Input
          id="invite-uses"
          type="number"
          min={1}
          max={1000}
          value={uses}
          onChange={(e) => setUses(e.target.value)}
          required
        />
      </div>
      <div className="flex w-44 flex-col gap-2">
        <Label htmlFor="invite-expires">Expires</Label>
        <Input
          id="invite-expires"
          type="date"
          min={today}
          max={inAYear}
          value={expires}
          aria-describedby="invite-expires-hint"
          onChange={(e) => setExpires(e.target.value)}
        />
        <p id="invite-expires-hint" className="text-xs text-muted-foreground">
          Empty: Invite lifetime from now.
        </p>
      </div>
      <Button type="submit" className="mt-6" disabled={create.isPending || Boolean(problem)}>
        <Plus />
        Make invite
      </Button>
    </form>
  )
}

/** Mailed, an invite is for its address alone, once (D180, D181, D371). */
function MailForm({ onCreated }: { onCreated: (invite: Invite) => void }) {
  const create = useCreateInvite()
  const [email, setEmail] = useState('')

  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        create.mutate(
          { email: email.trim() },
          {
            onSuccess: (invite) => {
              onCreated(invite)
              setEmail('')
              if (invite.sent) toast.success(`Invite mailed to ${invite.email}`)
            },
            onError: (error) => toast.error(errorMessage(error)),
          },
        )
      }}
    >
      <div className="flex min-w-64 flex-1 flex-col gap-2">
        <Label htmlFor="invite-email">Invite by email</Label>
        <Input
          id="invite-email"
          type="email"
          placeholder="someone@example.com"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          required
        />
      </div>
      <Button type="submit" variant="outline" disabled={create.isPending || !email.trim()}>
        Send invite
      </Button>
    </form>
  )
}

function InviteTable({ invites }: { invites: Invite[] }) {
  const revoke = useRevokeInvite()
  const [shown, setShown] = useState<Set<string>>(new Set())
  const [revoking, setRevoking] = useState<Invite>()

  const toggle = (id: string) =>
    setShown((s) => {
      const next = new Set(s)
      if (!next.delete(id)) next.add(id)
      return next
    })

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Code</TableHead>
            <TableHead>For</TableHead>
            <TableHead>Sign-ups</TableHead>
            <TableHead>Expires</TableHead>
            <TableHead>Status</TableHead>
            <TableHead className="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {invites.map((i) => {
            const status = statuses[i.status]
            const open = shown.has(i.id)

            return (
              <Fragment key={i.id}>
                <TableRow>
                  <TableCell>
                    {i.code ? (
                      <span className="flex items-center gap-1">
                        <code className="max-w-48 truncate font-mono text-xs">{i.code}</code>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          aria-label={`Copy the link of ${i.code}`}
                          title="Copy its link"
                          onClick={() => copyLink(i.code!)}
                        >
                          <Copy />
                        </Button>
                      </span>
                    ) : (
                      <span
                        className="text-xs text-muted-foreground"
                        title="Made before codes were kept"
                      >
                        Not kept
                      </span>
                    )}
                  </TableCell>
                  <TableCell>
                    {i.email || <span className="text-muted-foreground">Anyone with it</span>}
                  </TableCell>
                  <TableCell>
                    {i.used > 0 ? (
                      <Button
                        variant="ghost"
                        size="xs"
                        aria-expanded={open}
                        onClick={() => toggle(i.id)}
                      >
                        {open ? <ChevronDown /> : <ChevronRight />}
                        {i.used} of {i.maxUses}
                      </Button>
                    ) : (
                      <span className="px-2 text-muted-foreground">0 of {i.maxUses}</span>
                    )}
                  </TableCell>
                  <TableCell>{formatDateTime(i.expiresAt)}</TableCell>
                  <TableCell>
                    <Badge variant={status.variant}>{status.label}</Badge>
                  </TableCell>
                  <TableCell>
                    {i.status === 'open' && (
                      <Button variant="ghost" size="xs" onClick={() => setRevoking(i)}>
                        Revoke
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
                {open && (
                  <TableRow className="bg-muted/30 hover:bg-muted/30">
                    <TableCell colSpan={6}>
                      <ul
                        aria-label={`Who signed up with ${i.code ?? 'it'}`}
                        className="flex flex-col gap-1 py-1 text-xs"
                      >
                        {i.signups.map((u, n) => (
                          <li key={`${u.userId ?? 'gone'}-${n}`} className="flex gap-3">
                            <span className="w-44 text-muted-foreground">
                              {formatDateTime(u.at)}
                            </span>
                            <span>{u.email ?? 'A deleted account'}</span>
                          </li>
                        ))}
                      </ul>
                    </TableCell>
                  </TableRow>
                )}
              </Fragment>
            )
          })}
        </TableBody>
      </Table>

      <ConfirmDialog
        open={revoking !== undefined}
        title="Revoke this invite?"
        description="It admits no more sign-ups. Those who signed up with it keep their accounts, and it stays listed."
        confirm="Revoke"
        destructive
        onCancel={() => setRevoking(undefined)}
        onConfirm={() => {
          const id = revoking!.id
          setRevoking(undefined)
          revoke.mutate(id, {
            onSuccess: () => toast.success('Invite revoked'),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }}
      />
    </>
  )
}

const decisions: Record<Schemas['JoinRequest']['status'], string> = {
  pending: 'Waiting',
  approved: 'Approved',
  denied: 'Denied',
}

/**
 * Requests to join (D394): confirmed by their address, waiting for an admin, who approves,
 * mailing an invite there, or denies. Decided ones stay listed for 30 days.
 */
function Requests({ onApproved }: { onApproved: (invite: Invite) => void }) {
  const requests = useJoinRequests()
  const config = useAuthConfig()
  const approve = useApproveJoin()
  const deny = useDenyJoin()
  const [denying, setDenying] = useState<Schemas['JoinRequest']>()
  const list = requests.data?.requests ?? []
  const pending = requests.data?.pending ?? 0

  // Nothing to show while requests are off and none were made.
  if (requests.isSuccess && list.length === 0 && !config.data?.requests) return null

  return (
    <section className="flex flex-col gap-4">
      <div className="flex items-center gap-2">
        <h2 className="font-medium">Requests to join</h2>
        {pending > 0 && <Badge aria-label={`${pending} waiting`}>{pending}</Badge>}
      </div>
      {config.data && !config.data.requests && (
        <p className="text-sm text-muted-foreground">
          Requests are off: turn on{' '}
          <Link
            to={configPath('accounts', 'auth.requests')}
            className="text-foreground underline underline-offset-4"
          >
            Requests to join
          </Link>{' '}
          to take new ones.
        </p>
      )}

      {requests.isPending && <Skeleton className="h-16" />}
      {requests.isError && (
        <ErrorAlert error={requests.error} onRetry={() => void requests.refetch()} />
      )}
      {requests.isSuccess && list.length === 0 && (
        <p className="text-sm text-muted-foreground">No requests yet.</p>
      )}

      {list.length > 0 && (
        <ul aria-label="Requests to join" className="flex flex-col divide-y rounded-lg border">
          {list.map((r) => (
            <li key={r.id} className="flex flex-wrap items-start justify-between gap-3 p-3">
              <div className="flex min-w-0 flex-1 flex-col gap-1">
                <span className="text-sm font-medium">{r.email}</span>
                {r.note && (
                  <p className="text-sm whitespace-pre-wrap text-muted-foreground">{r.note}</p>
                )}
                <span className="text-xs text-muted-foreground">
                  Asked {formatDateTime(r.confirmedAt)}
                  {r.decidedAt &&
                    ` · ${decisions[r.status].toLowerCase()} ${formatDateTime(r.decidedAt)}`}
                </span>
              </div>
              {r.status === 'pending' ? (
                <div className="flex gap-2">
                  <Button
                    size="sm"
                    disabled={approve.isPending}
                    aria-label={`Approve ${r.email}`}
                    onClick={() =>
                      approve.mutate(r.id, {
                        onSuccess: (invite) => {
                          onApproved(invite)
                          if (invite.sent) toast.success(`Invite mailed to ${invite.email}`)
                        },
                        onError: (error) => toast.error(errorMessage(error)),
                      })
                    }
                  >
                    Approve
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    aria-label={`Deny ${r.email}`}
                    onClick={() => setDenying(r)}
                  >
                    Deny
                  </Button>
                </div>
              ) : (
                <Badge variant={r.status === 'approved' ? 'secondary' : 'outline'}>
                  {decisions[r.status]}
                </Badge>
              )}
            </li>
          ))}
        </ul>
      )}

      <ConfirmDialog
        open={denying !== undefined}
        title="Deny this request?"
        description="No invite is mailed. They may ask again in 30 days; while Tell denied requests is on, they are mailed so."
        confirm="Deny"
        destructive
        onCancel={() => setDenying(undefined)}
        onConfirm={() => {
          const id = denying!.id
          setDenying(undefined)
          deny.mutate(id, {
            onSuccess: () => toast.success('Request denied'),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }}
      />
    </section>
  )
}
