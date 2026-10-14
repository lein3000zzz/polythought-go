import { Copy, KeyRound } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'

import { useChangePassword, useCreateToken, useRevokeToken, useTokens } from '@/api/account'
import { useMinPasswordLength } from '@/api/auth'
import { ApiError, errorMessage, type Schemas } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { formatDateTime } from '@/lib/format'

export function SettingsPage() {
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-10 p-6">
      <h1 className="font-heading text-xl font-semibold">Settings</h1>
      <PasswordSection />
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
