import { toast } from 'sonner'

import { ApiError, errorMessage } from '@/api/client'
import { tokens, useAgentSpend, useSetAgentChoices } from '@/api/spend'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { formatDateTime } from '@/lib/format'

import { jumpId } from './pages'

/**
 * What your AI agent nodes spent this month with your own keys, the caps on it (D277),
 * and whether your kill switch stops them too (D276). Not there without agents.
 */
export function AgentsSection() {
  const spend = useAgentSpend()
  const choose = useSetAgentChoices()

  if (spend.error instanceof ApiError && spend.error.status === 404) return null

  const d = spend.data
  const tokenCap = (cap: number | undefined) => (cap ? `of ${tokens(cap)}` : 'no cap')

  return (
    <section id={jumpId('spend')} className="flex flex-col gap-4">
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

          <div id={jumpId('kill-switch')} className="flex items-start justify-between gap-4">
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
