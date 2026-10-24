import { Copy } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { useAgentSessions, useAgentSetup } from '@/api/oauth'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { formatDateTime } from '@/lib/format'

import { agentClients } from './agentClients'
import { jumpId } from './pages'

/**
 * Connect an agent (D414): the steps for each client an admin lists, the address filled in;
 * then the agent signs in through your browser, here.
 */
export function ConnectAgent() {
  const setup = useAgentSetup()
  const ordered = (setup.data?.clients ?? [])
    .map((id) => agentClients.find((c) => c.id === id))
    .filter((c) => c !== undefined)
  const [picked, setPicked] = useState<string>()
  const client = ordered.find((c) => c.id === picked) ?? ordered[0]

  return (
    <section id={jumpId('connect-agent')} className="flex flex-col gap-3">
      <div>
        <h3 className="font-medium">Connect an agent</h3>
        <p className="text-sm text-muted-foreground">
          Add Polythought to your agent, then sign in: your browser opens this app, which asks what
          the agent may do. Tick Take tasks for it to work your workflows&apos; Local agent nodes.
        </p>
      </div>

      {setup.isPending && <Skeleton className="h-32" />}
      {setup.isError && <ErrorAlert error={setup.error} onRetry={() => void setup.refetch()} />}

      {setup.data && !setup.data.enabled && (
        <p className="text-sm text-muted-foreground">
          An admin has turned agents off on this instance, on the Config page, Agents over MCP.
        </p>
      )}

      {setup.data?.enabled && client && (
        <>
          <div role="tablist" aria-label="Agent clients" className="flex flex-wrap gap-1">
            {ordered.map((c) => (
              <Button
                key={c.id}
                role="tab"
                aria-selected={c.id === client.id}
                variant={c.id === client.id ? 'secondary' : 'ghost'}
                size="sm"
                onClick={() => setPicked(c.id)}
              >
                {c.label}
              </Button>
            ))}
          </div>

          <ol role="tabpanel" aria-label={client.label} className="flex flex-col gap-3 text-sm">
            {client.steps(setup.data.address, setup.data.metadata).map((step, i) => (
              <li key={i} className="flex flex-col gap-1.5">
                <span>
                  {i + 1}. {step.text}
                </span>
                {step.code && <CodeBlock code={step.code} />}
              </li>
            ))}
          </ol>
        </>
      )}
    </section>
  )
}

function CodeBlock({ code }: { code: string }) {
  return (
    <div className="flex items-start gap-2">
      <pre className="min-w-0 flex-1 overflow-x-auto rounded-md bg-muted p-2 font-mono text-xs">
        {code}
      </pre>
      <Button
        variant="outline"
        size="icon"
        aria-label="Copy"
        onClick={() => void navigator.clipboard.writeText(code).then(() => toast.success('Copied'))}
      >
        <Copy />
      </Button>
    </div>
  )
}

/**
 * Your agents and tokens connected now (D414): their sessions open on the server, and
 * those it had as it restarted, until their agents come back.
 */
export function ConnectedNow() {
  const sessions = useAgentSessions()

  return (
    <section id={jumpId('connected-now')} className="flex flex-col gap-2">
      <h3 className="font-medium">Connected now</h3>

      {sessions.isPending && <Skeleton className="h-12" />}
      {sessions.isError && (
        <ErrorAlert error={sessions.error} onRetry={() => void sessions.refetch()} />
      )}
      {sessions.data?.length === 0 && (
        <p className="text-sm text-muted-foreground">None of your agents is connected.</p>
      )}

      {sessions.data && sessions.data.length > 0 && (
        <ul aria-label="Connected now" className="flex flex-col gap-1.5 text-sm">
          {sessions.data.map((s, i) => (
            <li key={i} className="flex flex-wrap items-center gap-2">
              <span className="font-medium">
                {s.kind === 'token' ? `The API token “${s.name}”` : s.name}
              </span>
              {s.client && <span className="text-muted-foreground">{s.client}</span>}
              {s.beforeRestart && (
                <Badge
                  variant="outline"
                  title="It was connected as the server restarted; it reconnects when it next calls"
                >
                  Since before a restart
                </Badge>
              )}
              <span className="text-xs text-muted-foreground">
                since {formatDateTime(s.openedAt)}, last call {formatDateTime(s.seenAt)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
