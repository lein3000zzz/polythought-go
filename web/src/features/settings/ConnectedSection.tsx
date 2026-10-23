import { useState } from 'react'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { errorMessage, type Schemas } from '@/api/client'
import { useGrants, useRevokeGrant, useSetAgentAccess } from '@/api/oauth'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
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
import { scopeTitle } from '@/features/oauth/scopes'
import { formatDateTime } from '@/lib/format'

import { jumpId } from './pages'

/** The agents the user allowed, with what each may do, to revoke (D384). */
export function ConnectedSection() {
  const grants = useGrants()
  const revoke = useRevokeGrant()
  const me = useMe()
  const access = useSetAgentAccess()
  const [revoking, setRevoking] = useState<Schemas['OAuthGrant']>()

  return (
    <section id={jumpId('connected')} className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">Connected agents</h2>
        <p className="text-sm text-muted-foreground">
          Agents such as Claude Code and Codex that you allowed to work with Polythought as you,
          over MCP. Each may do what you ticked when it asked. Revoke one and it stops at once; it
          asks you again the next time it signs in.
        </p>
      </div>

      <div id={jumpId('agents-see-personal')} className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-0.5">
          <Label htmlFor="agents-see-personal">Agents and tokens see personal settings</Label>
          <p id="agents-see-personal-hint" className="text-xs text-muted-foreground">
            Settings such as a webhook's URL, which can be a secret. Off, agents and API tokens see
            that they are set, never what they are, as sharing a workflow leaves them out; a
            workflow they save keeps them as they were.
          </p>
        </div>
        <Switch
          id="agents-see-personal"
          aria-describedby="agents-see-personal-hint"
          checked={me.data?.agentsSeePersonal ?? false}
          disabled={access.isPending || !me.data}
          onCheckedChange={(on) =>
            access.mutate(
              { seePersonal: on },
              {
                onSuccess: () =>
                  toast.success(
                    on
                      ? 'Your agents and tokens see personal settings'
                      : 'Your agents and tokens no longer see personal settings',
                  ),
                onError: (error) => toast.error(errorMessage(error)),
              },
            )
          }
        />
      </div>

      {grants.isPending && <Skeleton className="h-24" />}
      {grants.isError && <ErrorAlert error={grants.error} onRetry={() => void grants.refetch()} />}
      {grants.data?.length === 0 && <p className="text-sm text-muted-foreground">No agents yet.</p>}

      {grants.data && grants.data.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Agent</TableHead>
              <TableHead>May</TableHead>
              <TableHead>Allowed</TableHead>
              <TableHead>Last used</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {grants.data.map((g) => (
              <TableRow
                key={g.id}
                id={jumpId(`grant-${g.id}`)}
                className={g.revokedAt ? 'text-muted-foreground' : undefined}
              >
                <TableCell>
                  <div className="flex flex-wrap items-center gap-1.5 font-medium">
                    {g.client.name}
                    {!g.client.verified && <Badge variant="outline">Unverified</Badge>}
                  </div>
                  {g.client.site && (
                    <div className="text-xs text-muted-foreground">{g.client.site}</div>
                  )}
                </TableCell>
                <TableCell>{g.scopes.map(scopeTitle).join(', ')}</TableCell>
                <TableCell>{formatDateTime(g.createdAt)}</TableCell>
                <TableCell>{g.lastUsedAt ? formatDateTime(g.lastUsedAt) : 'Never'}</TableCell>
                <TableCell className="text-right">
                  {g.revokedAt ? (
                    `Revoked ${formatDateTime(g.revokedAt)}`
                  ) : (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-destructive"
                      onClick={() => setRevoking(g)}
                    >
                      Revoke
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <ConfirmDialog
        open={revoking !== undefined}
        title={`Revoke “${revoking?.client.name}”?`}
        description="It stops working at once, wherever it runs, and asks you again the next time it signs in."
        confirm="Revoke"
        destructive
        onCancel={() => setRevoking(undefined)}
        onConfirm={() =>
          revoking &&
          revoke.mutate(revoking.id, {
            onSuccess: () => {
              setRevoking(undefined)
              toast.success('Agent revoked')
            },
            onError: (error) => toast.error(errorMessage(error)),
          })
        }
      />
    </section>
  )
}
