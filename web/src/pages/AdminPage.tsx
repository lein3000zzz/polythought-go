import { Copy, Plus } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'

import { useCreateInvite, useCreateUser, useInvites, useUpdateUser, useUsers } from '@/api/admin'
import { useAuthConfig, useMe } from '@/api/auth'
import { errorMessage, type Schemas } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatDateTime } from '@/lib/format'

type User = Schemas['User']

// The server's minimum, repeated here only to say it up front.
const minPasswordLength = 12

const registrationModes: Record<Schemas['AuthConfig']['registration'], string> = {
  open: 'Anyone can create an account.',
  invite: 'People need an invite to create an account.',
  closed: 'Only admins create accounts.',
}

export function AdminPage() {
  const { data: me } = useMe()

  if (me?.role !== 'admin') {
    return <p className="p-6 text-sm text-muted-foreground">This page is for admins.</p>
  }

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-10 p-6">
      <h1 className="font-heading text-xl font-semibold">Admin</h1>
      <UsersSection me={me} />
      <InvitesSection />
    </div>
  )
}

function UsersSection({ me }: { me: User }) {
  const users = useUsers()
  const update = useUpdateUser()
  const [change, setChange] = useState<{ user: User; patch: Schemas['UpdateUserRequest'] }>()
  const items = users.data?.pages.flatMap((page) => page.users) ?? []

  const confirmText = (user: User, patch: Schemas['UpdateUserRequest']) => {
    if (patch.disabled) {
      return [
        'Disable',
        `${user.email} is signed out, their API tokens stop working and they cannot sign in. Their active workflows are turned off and stay off if you enable them again.`,
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
                  </Badge>
                </TableCell>
                <TableCell>{formatDateTime(u.createdAt)}</TableCell>
                <TableCell className="text-right">
                  {u.id !== me.id && (
                    <div className="flex justify-end gap-1">
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

type CreateUserForm = { email: string; password: string; role: 'user' | 'admin' }

function CreateUserForm() {
  const create = useCreateUser()
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
            placeholder={`At least ${minPasswordLength} characters`}
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

function InvitesSection() {
  const config = useAuthConfig()
  const invites = useInvites()
  const create = useCreateInvite()
  const [created, setCreated] = useState<Schemas['Invite']>()
  const [now] = useState(() => Date.now()) // when the page opened, to tell expired invites

  const link = created?.code
    ? `${window.location.origin}/register?invite=${encodeURIComponent(created.code)}`
    : undefined

  return (
    <section className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="font-medium">Invites</h2>
          {config.data && (
            <p className="text-sm text-muted-foreground">
              {registrationModes[config.data.registration]} The server's POLYTHOUGHT_REGISTRATION
              setting decides this.
            </p>
          )}
        </div>
        <Button
          variant="outline"
          disabled={create.isPending}
          onClick={() =>
            create.mutate(undefined, {
              onSuccess: setCreated,
              onError: (error) => toast.error(errorMessage(error)),
            })
          }
        >
          <Plus />
          Create invite
        </Button>
      </div>

      {link && created && (
        <Alert>
          <AlertTitle>Send this link now</AlertTitle>
          <AlertDescription className="flex flex-col gap-2">
            <p>
              It works once, until {formatDateTime(created.expiresAt)}, and is shown only this once.
            </p>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 font-mono text-xs">
                {link}
              </code>
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  void navigator.clipboard
                    .writeText(link)
                    .then(() => toast.success('Invite link copied'))
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

      {invites.isPending && <Skeleton className="h-24" />}
      {invites.isError && (
        <ErrorAlert error={invites.error} onRetry={() => void invites.refetch()} />
      )}
      {invites.data?.length === 0 && (
        <p className="text-sm text-muted-foreground">No invites yet.</p>
      )}

      {invites.data && invites.data.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Created</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead>Status</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {invites.data.map((i) => (
              <TableRow key={i.id}>
                <TableCell>{formatDateTime(i.createdAt)}</TableCell>
                <TableCell>{formatDateTime(i.expiresAt)}</TableCell>
                <TableCell>
                  {i.usedAt ? (
                    <Badge variant="secondary">Used {formatDateTime(i.usedAt)}</Badge>
                  ) : Date.parse(i.expiresAt) < now ? (
                    <Badge variant="outline">Expired</Badge>
                  ) : (
                    <Badge>Open</Badge>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}
