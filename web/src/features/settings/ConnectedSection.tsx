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

import { ConfirmationsSection } from './ConfirmationsSection'
import { ConnectAgent, ConnectedNow } from './ConnectAgent'
import { jumpId } from './pages'

/** The agents the user allowed, with what each may do, to revoke (D384). */
export function ConnectedSection() {
  const grants = useGrants()
  const revoke = useRevokeGrant()
  const me = useMe()
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

      <ConnectAgent />
      <ConnectedNow />

      <AccessSwitch
        id="agents-see-personal"
        label="Agents and tokens see personal settings"
        hint="Settings such as a webhook's URL, which can be a secret. Off, agents and API tokens see that they are set, never what they are, as sharing a workflow leaves them out; a workflow they save keeps them as they were."
        checked={me.data?.agentsSeePersonal}
        change={(on) => ({ seePersonal: on })}
        said={(on) =>
          on
            ? 'Your agents and tokens see personal settings'
            : 'Your agents and tokens no longer see personal settings'
        }
      />
      <AccessSwitch
        id="agents-may-delete"
        label="Agents and tokens may delete"
        hint="Workflows, with their executions; custom nodes, which the workflows using them then lack; and credentials, whose secrets are gone. None can be had back. Off, they change these, but never delete one."
        checked={me.data?.agentsMayDelete}
        change={(on) => ({ delete: on })}
        said={(on) =>
          on ? 'Your agents and tokens may delete' : 'Your agents and tokens may no longer delete'
        }
      />
      <AccessSwitch
        id="agents-may-share"
        label="Agents and tokens may share to the library"
        hint="A shared workflow is there for everyone on this instance to copy, its personal settings left out. Off, they start from the library's workflows, but never share one."
        checked={me.data?.agentsMayShare}
        change={(on) => ({ share: on })}
        said={(on) =>
          on
            ? 'Your agents and tokens may share to the library'
            : 'Your agents and tokens may no longer share to the library'
        }
      />
      <AccessSwitch
        id="agents-use-credentials"
        label="Agents and tokens may use my credentials in workflows"
        hint="A workflow they build can send a credential wherever its nodes call, such as a header to any webhook. Off, they list your credentials and add ones you give them, but do not save, switch on or run a workflow that uses one, test one, or change where one goes."
        checked={me.data?.agentsMayUseCredentials}
        change={(on) => ({ useCredentials: on })}
        said={(on) =>
          on
            ? 'Your agents and tokens may use your credentials'
            : 'Your agents and tokens may no longer use your credentials'
        }
      />
      <AccessSwitch
        id="agents-may-answer"
        label="Agents and tokens may answer my workflows' questions"
        hint="Questions such as a Telegram question, which can be how you approve a trade. Off, they see what a run asks and may cancel it, but never answer; on, the question says which agent answered."
        checked={me.data?.agentsMayAnswer}
        change={(on) => ({ answer: on })}
        said={(on) =>
          on
            ? 'Your agents and tokens may answer your questions'
            : 'Your agents and tokens may no longer answer your questions'
        }
      />

      <ConfirmationsSection />

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
                    {!g.client.published && <Badge variant="outline">Unverified</Badge>}
                    {g.takingTasks && !g.revokedAt && (
                      <Badge
                        variant="secondary"
                        title="It waits for your Local agent nodes' tasks now, or did a moment ago"
                      >
                        Taking tasks
                      </Badge>
                    )}
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

/** One of the switches for what the user's agents and tokens may see and do (D401, D403). */
function AccessSwitch({
  id,
  label,
  hint,
  checked,
  change,
  said,
}: {
  id: string
  label: string
  hint: string
  checked: boolean | undefined
  change: (on: boolean) => Schemas['AgentAccessRequest']
  said: (on: boolean) => string
}) {
  const access = useSetAgentAccess()

  return (
    <div id={jumpId(id)} className="flex items-start justify-between gap-4">
      <div className="flex flex-col gap-0.5">
        <Label htmlFor={id}>{label}</Label>
        <p id={`${id}-hint`} className="text-xs text-muted-foreground">
          {hint}
        </p>
      </div>
      <Switch
        id={id}
        aria-describedby={`${id}-hint`}
        checked={checked ?? false}
        disabled={access.isPending || checked === undefined}
        onCheckedChange={(on) =>
          access.mutate(change(on), {
            onSuccess: () => toast.success(said(on)),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }
      />
    </div>
  )
}
