import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { useCreateUser, useSendVerificationLink, useUpdateUser, useUsers } from '@/api/admin'
import { useMe, useMinPasswordLength } from '@/api/auth'
import { errorMessage, type Schemas } from '@/api/client'
import { useSettings } from '@/api/settings'
import { type AgentCaps, tokens, useUserAgentSpend } from '@/api/spend'
import { AdminHeader } from '@/components/AdminHeader'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
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
import { configPath } from '@/features/config/pages'
import { formatDateTime } from '@/lib/format'

type User = Schemas['User']

type CodeAccess = 'everyone' | 'chosen' | 'off'

// Who may write code, as the Code access setting on the Config page says.
const codeAccessModes: Record<CodeAccess, string> = {
  everyone: 'Everyone may write code.',
  chosen: 'Admins may write code, and the users switched on below.',
  off: 'Code is off for everyone, admins included.',
}

export function AdminPage() {
  const { data: me } = useMe()

  if (me?.role !== 'admin') {
    return <p className="p-6 text-sm text-muted-foreground">This page is for admins.</p>
  }

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-10 p-6">
      <AdminHeader />
      <UsersSection me={me} />
    </div>
  )
}

function UsersSection({ me }: { me: User }) {
  const users = useUsers()
  const update = useUpdateUser()
  const sendVerification = useSendVerificationLink()
  const [change, setChange] = useState<{ user: User; patch: Schemas['UpdateUserRequest'] }>()
  const [capping, setCapping] = useState<User>()
  const [agentCapping, setAgentCapping] = useState<User>()
  const items = users.data?.pages.flatMap((page) => page.users) ?? []
  const settings = useSettings()
  const codeAccess = settings.data?.settings.find((s) => s.key === 'code.access')?.value as
    CodeAccess | undefined
  const instanceCaps = {
    perOrder: settings.data?.settings.find((s) => s.key === 'trading.max_live_order')?.value as
      number | undefined,
    perDay: settings.data?.settings.find((s) => s.key === 'trading.max_live_day')?.value as
      number | undefined,
  }
  const setting = (key: string) => settings.data?.settings.find((s) => s.key === key)?.value
  const instanceAgentCaps: AgentCaps = {
    dollars: setting('agents.max_dollars_month') as string | undefined,
    inputTokens: setting('agents.max_input_month') as number | undefined,
    outputTokens: setting('agents.max_output_month') as number | undefined,
    totalTokens: setting('agents.max_total_month') as number | undefined,
  }

  const chooseForCode = (user: User, chosen: boolean) =>
    update.mutate(
      { id: user.id, codeChosen: chosen },
      {
        onSuccess: () =>
          toast.success(
            chosen ? `${user.email} may write code` : `${user.email} may no longer write code`,
          ),
        onError: (error) => toast.error(errorMessage(error)),
      },
    )

  // An admin's word for an address, when the mail does not get through (D175).
  const markVerified = (user: User) =>
    update.mutate(
      { id: user.id, emailVerified: true },
      {
        onSuccess: () => toast.success(`${user.email} is verified`),
        onError: (error) => toast.error(errorMessage(error)),
      },
    )

  const sendLink = (user: User) =>
    sendVerification.mutate(user.id, {
      onSuccess: () => toast.success(`A new link is on its way to ${user.email}`),
      onError: (error) => toast.error(errorMessage(error)),
    })

  const confirmText = (user: User, patch: Schemas['UpdateUserRequest']) => {
    if (patch.disabled) {
      return [
        'Disable',
        `${user.email} is signed out, their API tokens stop working and they cannot sign in. Their active workflows are turned off and stay off if you enable them again. Their live trading stops: their kill switch goes on, cancelling their resting live orders, and stays on until they turn it off.`,
      ]
    }
    if (patch.disabled === false) {
      return [
        'Enable',
        `${user.email} can sign in again. Their workflows stay off until they turn them on.`,
      ]
    }
    if (patch.role === 'admin') return ['Make admin', `${user.email} can manage every account.`]

    return ['Make user', `${user.email} loses access to this page.`]
  }

  return (
    <section className="flex flex-col gap-4">
      <h2 className="font-medium">Users</h2>

      {codeAccess && (
        <p className="text-sm text-muted-foreground">
          {codeAccessModes[codeAccess]} Who may is set under Code on the{' '}
          <Link to={configPath('code', 'code.access')} className="underline underline-offset-4">
            Config page
          </Link>
          .
        </p>
      )}

      <CreateUserForm />

      {users.isPending && <Skeleton className="h-32" />}
      {users.isError && <ErrorAlert error={users.error} onRetry={() => void users.refetch()} />}

      {items.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Email</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Code</TableHead>
              <TableHead>Live caps</TableHead>
              <TableHead>Agent caps</TableHead>
              <TableHead>Created</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((u) => (
              <TableRow key={u.id}>
                <TableCell className="font-medium">
                  {u.email} {u.id === me.id && <Badge variant="outline">You</Badge>}
                </TableCell>
                <TableCell>{u.role === 'admin' ? 'Admin' : 'User'}</TableCell>
                <TableCell>
                  <Badge variant={u.disabled ? 'destructive' : 'secondary'}>
                    {u.disabled ? 'Disabled' : 'Active'}
                  </Badge>{' '}
                  {!u.emailVerified && <Badge variant="outline">Unverified</Badge>}
                  {u.liveStopped && <Badge variant="destructive">Live stopped</Badge>}
                </TableCell>
                <TableCell>
                  {codeAccess === 'chosen' && u.role !== 'admin' ? (
                    <Switch
                      size="sm"
                      checked={u.codeChosen}
                      aria-label={`${u.email} may write code`}
                      disabled={update.isPending}
                      onCheckedChange={(chosen) => chooseForCode(u, chosen)}
                    />
                  ) : u.mayWriteCode ? (
                    'Yes'
                  ) : (
                    'No'
                  )}
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="-ml-2 font-normal"
                    aria-label={`Live caps of ${u.email}`}
                    onClick={() => setCapping(u)}
                  >
                    {capsText(u.liveCaps)}
                  </Button>
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="-ml-2 font-normal"
                    aria-label={`Agent caps of ${u.email}`}
                    onClick={() => setAgentCapping(u)}
                  >
                    {agentCapsText(u.agentCaps)}
                  </Button>
                </TableCell>
                <TableCell>{formatDateTime(u.createdAt)}</TableCell>
                <TableCell className="text-right">
                  {u.id !== me.id && (
                    <div className="flex justify-end gap-1">
                      {!u.emailVerified && (
                        <>
                          <Button variant="ghost" size="sm" onClick={() => sendLink(u)}>
                            Send link
                          </Button>
                          <Button variant="ghost" size="sm" onClick={() => markVerified(u)}>
                            Mark verified
                          </Button>
                        </>
                      )}
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() =>
                          setChange({
                            user: u,
                            patch: { role: u.role === 'admin' ? 'user' : 'admin' },
                          })
                        }
                      >
                        {u.role === 'admin' ? 'Make user' : 'Make admin'}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        className={u.disabled ? undefined : 'text-destructive'}
                        onClick={() => setChange({ user: u, patch: { disabled: !u.disabled } })}
                      >
                        {u.disabled ? 'Enable' : 'Disable'}
                      </Button>
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {users.hasNextPage && (
        <Button
          variant="outline"
          className="self-center"
          onClick={() => void users.fetchNextPage()}
          disabled={users.isFetchingNextPage}
        >
          Load more
        </Button>
      )}

      {capping && (
        <CapsDialog user={capping} instance={instanceCaps} onClose={() => setCapping(undefined)} />
      )}

      {agentCapping && (
        <AgentCapsDialog
          user={agentCapping}
          instance={instanceAgentCaps}
          onClose={() => setAgentCapping(undefined)}
        />
      )}

      {change && (
        <ConfirmDialog
          open
          title={`${confirmText(change.user, change.patch)[0]}?`}
          description={confirmText(change.user, change.patch)[1]}
          confirm={confirmText(change.user, change.patch)[0]}
          destructive={change.patch.disabled === true || change.patch.role === 'user'}
          onCancel={() => setChange(undefined)}
          onConfirm={() =>
            update.mutate(
              { id: change.user.id, ...change.patch },
              {
                onSuccess: () => {
                  setChange(undefined)
                  toast.success('User updated')
                },
                onError: (error) => {
                  setChange(undefined)
                  toast.error(errorMessage(error))
                },
              },
            )
          }
        />
      )}
    </section>
  )
}

type LiveCaps = Schemas['LiveCaps']

/** A user's caps in the table: theirs, or the instance's. */
function capsText(caps: LiveCaps): string {
  if (caps.perOrder === undefined && caps.perDay === undefined) return 'Default'

  const one = (cap: number | undefined, per: string) =>
    cap === undefined
      ? `default ${per}`
      : cap === 0
        ? `no cap ${per}`
        : `$${cap.toLocaleString('en-US')} ${per}`

  return `${one(caps.perOrder, 'an order')}, ${one(caps.perDay, 'a day')}`
}

/** An admin's caps for one user's live buys (D222): empty is the instance's, 0 none. */
function CapsDialog({
  user,
  instance,
  onClose,
}: {
  user: User
  instance: LiveCaps
  onClose: () => void
}) {
  const update = useUpdateUser()
  const [perOrder, setPerOrder] = useState(user.liveCaps.perOrder?.toString() ?? '')
  const [perDay, setPerDay] = useState(user.liveCaps.perDay?.toString() ?? '')

  const read = (text: string) => (text.trim() === '' ? undefined : Number(text))
  const placeholder = (cap: number | undefined) =>
    cap === undefined
      ? 'Default'
      : cap === 0
        ? 'Default: no cap'
        : `Default: $${cap.toLocaleString('en-US')}`

  return (
    <Dialog open onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Live caps of {user.email}</DialogTitle>
          <DialogDescription>
            What their live buys may spend, in whole dollars, over the Config page&apos;s caps.
            Empty is the instance&apos;s cap, 0 none. A buy over a cap is refused before it is sent.
          </DialogDescription>
        </DialogHeader>

        <form
          id="live-caps"
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            update.mutate(
              { id: user.id, liveCaps: { perOrder: read(perOrder), perDay: read(perDay) } },
              {
                onSuccess: () => {
                  toast.success(`Live caps of ${user.email} saved`)
                  onClose()
                },
              },
            )
          }}
        >
          {update.isError && <ErrorAlert error={update.error} />}

          <div className="flex flex-col gap-2">
            <Label htmlFor="cap-order">Max $ a live order</Label>
            <Input
              id="cap-order"
              type="number"
              min={0}
              max={10_000_000}
              step={1}
              value={perOrder}
              placeholder={placeholder(instance.perOrder)}
              onChange={(e) => setPerOrder(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="cap-day">Max $ a day of live buys</Label>
            <Input
              id="cap-day"
              type="number"
              min={0}
              max={10_000_000}
              step={1}
              value={perDay}
              placeholder={placeholder(instance.perDay)}
              onChange={(e) => setPerDay(e.target.value)}
            />
          </div>
        </form>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="live-caps" disabled={update.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type CreateUserForm = { email: string; password: string; role: 'user' | 'admin' }

function CreateUserForm() {
  const create = useCreateUser()
  const minPasswordLength = useMinPasswordLength()
  const { register, handleSubmit, reset } = useForm<CreateUserForm>({
    defaultValues: { role: 'user' },
  })

  return (
    <form
      className="flex flex-col gap-3 rounded-lg border p-4"
      onSubmit={handleSubmit((body) =>
        create.mutate(body, {
          onSuccess: (u) => {
            reset()
            toast.success(`Created ${u.email}`)
          },
        }),
      )}
    >
      <h3 className="text-sm font-medium">Create a user</h3>
      {create.isError && <ErrorAlert error={create.error} />}

      <div className="flex flex-wrap items-end gap-3">
        <div className="flex min-w-56 flex-1 flex-col gap-2">
          <Label htmlFor="new-user-email">Email</Label>
          <Input
            id="new-user-email"
            type="email"
            autoComplete="off"
            required
            {...register('email')}
          />
        </div>
        <div className="flex min-w-48 flex-col gap-2">
          <Label htmlFor="new-user-password">Password</Label>
          <Input
            id="new-user-password"
            type="password"
            autoComplete="new-password"
            minLength={minPasswordLength}
            placeholder={minPasswordLength ? `At least ${minPasswordLength} characters` : undefined}
            required
            {...register('password')}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="new-user-role">Role</Label>
          <NativeSelect id="new-user-role" {...register('role')}>
            <NativeSelectOption value="user">User</NativeSelectOption>
            <NativeSelectOption value="admin">Admin</NativeSelectOption>
          </NativeSelect>
        </div>
        <Button type="submit" disabled={create.isPending}>
          Create
        </Button>
      </div>
    </form>
  )
}

/** A user's agent caps in the table: theirs, or the instance's (D271). */
function agentCapsText(caps: AgentCaps): string {
  const set = [
    caps.dollars !== undefined &&
      (Number(caps.dollars) === 0 ? 'no $ cap' : `$${caps.dollars} a month`),
    caps.totalTokens !== undefined &&
      (caps.totalTokens === 0 ? 'no token cap' : `${tokens(caps.totalTokens)} tokens`),
    caps.inputTokens !== undefined &&
      (caps.inputTokens === 0 ? 'no input cap' : `${tokens(caps.inputTokens)} in`),
    caps.outputTokens !== undefined &&
      (caps.outputTokens === 0 ? 'no output cap' : `${tokens(caps.outputTokens)} out`),
  ].filter(Boolean)

  return set.length === 0 ? 'Default' : set.join(', ')
}

/**
 * An admin's caps on one user's agent spend a month (D271): empty is the instance's, 0
 * none. Beside them, what the user's agents spent this month (D277).
 */
function AgentCapsDialog({
  user,
  instance,
  onClose,
}: {
  user: User
  instance: AgentCaps
  onClose: () => void
}) {
  const update = useUpdateUser()
  const spend = useUserAgentSpend(user.id)
  const [dollars, setDollars] = useState(user.agentCaps.dollars ?? '')
  const [total, setTotal] = useState(user.agentCaps.totalTokens?.toString() ?? '')
  const [input, setInput] = useState(user.agentCaps.inputTokens?.toString() ?? '')
  const [output, setOutput] = useState(user.agentCaps.outputTokens?.toString() ?? '')

  const count = (text: string) => (text.trim() === '' ? undefined : Number(text))
  const placeholder = (cap: number | string | undefined, show: (n: number) => string) =>
    cap === undefined
      ? 'Default'
      : Number(cap) === 0
        ? 'Default: no cap'
        : `Default: ${show(Number(cap))}`
  const d = spend.data

  return (
    <Dialog open onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Agent caps of {user.email}</DialogTitle>
          <DialogDescription>
            What their AI agent nodes may spend in a calendar month, UTC, over the Config
            page&apos;s caps. Empty is the instance&apos;s cap, 0 none. Dollars count what providers
            report, which only some do; tokens count every call.
          </DialogDescription>
        </DialogHeader>

        {d && (
          <p className="text-sm" aria-label="Their spend this month">
            This month: ${d.dollars} ({d.costedCalls} of {d.calls} calls priced),{' '}
            {tokens(d.totalTokens)} tokens ({tokens(d.inputTokens)} in, {tokens(d.outputTokens)}{' '}
            out).
          </p>
        )}
        {spend.isError && <ErrorAlert error={spend.error} />}

        <form
          id="agent-caps"
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            update.mutate(
              {
                id: user.id,
                agentCaps: {
                  dollars: dollars.trim() === '' ? undefined : dollars.trim(),
                  totalTokens: count(total),
                  inputTokens: count(input),
                  outputTokens: count(output),
                },
              },
              {
                onSuccess: () => {
                  toast.success(`Agent caps of ${user.email} saved`)
                  onClose()
                },
              },
            )
          }}
        >
          {update.isError && <ErrorAlert error={update.error} />}

          <div className="flex flex-col gap-2">
            <Label htmlFor="agent-dollars">Max $ a month</Label>
            <Input
              id="agent-dollars"
              inputMode="decimal"
              value={dollars}
              placeholder={placeholder(instance.dollars, (n) => `$${n.toFixed(2)}`)}
              onChange={(e) => setDollars(e.target.value)}
            />
          </div>

          {(
            [
              ['agent-total', 'Max tokens a month', total, setTotal, instance.totalTokens],
              ['agent-input', 'Max input tokens a month', input, setInput, instance.inputTokens],
              [
                'agent-output',
                'Max output tokens a month',
                output,
                setOutput,
                instance.outputTokens,
              ],
            ] as const
          ).map(([id, label, value, set, cap]) => (
            <div key={id} className="flex flex-col gap-2">
              <Label htmlFor={id}>{label}</Label>
              <Input
                id={id}
                type="number"
                min={0}
                step={1}
                value={value}
                placeholder={placeholder(cap, tokens)}
                onChange={(e) => set(e.target.value)}
              />
            </div>
          ))}
        </form>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="agent-caps" disabled={update.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
