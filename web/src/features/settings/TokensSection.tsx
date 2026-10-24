import { Copy, KeyRound } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'

import { useCreateToken, useRevokeToken, useTokens } from '@/api/account'
import { useMe } from '@/api/auth'
import { errorMessage, type Schemas } from '@/api/client'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
import { scopeInfo, scopeTitle } from '@/features/oauth/scopes'
import { formatDateTime } from '@/lib/format'

import { jumpId } from './pages'

type TokenForm = { name: string; expires: string }
type Scope = Schemas['CreateTokenRequest']['scopes'][number]

const scopes: Scope[] = ['read', 'build', 'tasks', 'settings', 'paper', 'live', 'admin']
// What a new token may do to start with (D398, D399, D412): tasks, settings, live trading
// and admin are ticked by hand.
const firstTicked: Scope[] = ['read', 'build', 'paper']

export function TokensSection() {
  const tokens = useTokens()
  const create = useCreateToken()
  const [created, setCreated] = useState<Schemas['APIToken']>()
  const [revoking, setRevoking] = useState<Schemas['APIToken']>()
  const revoke = useRevokeToken()
  const { register, handleSubmit, reset } = useForm<TokenForm>()
  const me = useMe()
  const offered = scopes.filter((sc) => sc !== 'admin' || me.data?.role === 'admin')
  const [ticked, setTicked] = useState(() => new Set<Scope>(firstTicked))
  const tick = (sc: Scope, on: boolean) =>
    setTicked((was) => {
      const next = new Set(was)
      if (on) next.add(sc)
      else next.delete(sc)
      return next
    })
  // The date picker's first day, in the user's time zone (en-CA writes YYYY-MM-DD).
  const [today] = useState(() => new Date().toLocaleDateString('en-CA'))

  return (
    <section className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">API tokens</h2>
        <p className="text-sm text-muted-foreground">
          For scripts, tools and agents: send{' '}
          <code className="font-mono text-xs">Authorization: Bearer pt_…</code> with requests to{' '}
          <code className="font-mono text-xs">/api/v1</code>, or to{' '}
          <code className="font-mono text-xs">/mcp</code> from an agent. A token may do only what
          its scopes allow, there and over MCP alike, and never change your password, create or
          revoke tokens, store wallet credentials or allow an agent.
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
        id={jumpId('new-token')}
        className="flex flex-wrap items-end gap-3 rounded-lg border p-4"
        onSubmit={handleSubmit(({ name, expires }) =>
          create.mutate(
            {
              name,
              // The end of the chosen day, in the user's time zone.
              ...(expires ? { expiresAt: new Date(`${expires}T23:59:59`).toISOString() } : {}),
              scopes: offered.filter((sc) => ticked.has(sc)),
            },
            {
              onSuccess: (token) => {
                reset()
                setTicked(new Set(firstTicked))
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
        <Button type="submit" disabled={create.isPending || ticked.size === 0}>
          Create token
        </Button>
        <fieldset className="flex w-full flex-wrap gap-x-4 gap-y-2">
          <legend className="mb-2 text-sm font-medium">It may</legend>
          {offered.map((sc) => (
            <label
              key={sc}
              className="flex items-center gap-2 text-sm"
              title={scopeInfo[sc]?.detail}
            >
              <Checkbox checked={ticked.has(sc)} onCheckedChange={(on) => tick(sc, on === true)} />
              {scopeTitle(sc)}
            </label>
          ))}
        </fieldset>
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
              <TableHead>May</TableHead>
              <TableHead>Created</TableHead>
              <TableHead>Last used</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {tokens.data.map((t) => (
              <TableRow key={t.id} id={jumpId(`token-${t.id}`)}>
                <TableCell className="font-medium">{t.name}</TableCell>
                <TableCell className="font-mono text-xs">{t.prefix}…</TableCell>
                <TableCell>{t.scopes ? t.scopes.map(scopeTitle).join(', ') : 'All'}</TableCell>
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
