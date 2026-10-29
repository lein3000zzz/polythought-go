import {
  type SwitchKind,
  switchKinds,
  useWorkflowHistory,
  type WorkflowSwitch,
} from '@/api/history'
import { useWallets } from '@/api/wallets'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { actorName } from '@/lib/actors'
import { formatDateTime, shortAddress } from '@/lib/format'

type Props = { workflowId: string; kinds: SwitchKind[] }

/**
 * Who switched the workflow and why (D433), newest first: on and off, paper or live and
 * from which wallet, its paper account reset, and the kill switch as it traded live.
 */
export function SwitchList({ workflowId, kinds }: Props) {
  // All of them asked as none: one list in the cache, however they were ticked.
  const asked = kinds.length === switchKinds.length ? [] : kinds
  const history = useWorkflowHistory(workflowId, asked)
  const wallets = useWallets()
  const switches = history.data?.pages.flatMap((p) => p.switches) ?? []

  const walletName = (w: NonNullable<WorkflowSwitch['wallet']>) =>
    wallets.data?.find((x) => x.id === w.id)?.label || shortAddress(w.address)

  if (kinds.length === 0) {
    return <p className="p-4 text-sm text-muted-foreground">Choose what to show, above.</p>
  }

  return (
    <>
      {history.isError && (
        <div className="p-3">
          <ErrorAlert error={history.error} onRetry={() => void history.refetch()} />
        </div>
      )}

      {history.isSuccess && switches.length === 0 && (
        <p className="p-4 text-sm text-muted-foreground">
          Nothing yet. Who switches it on or off, to paper or live, resets its paper account or
          turns the kill switch shows here, and why.
        </p>
      )}

      <ul>
        {switches.map((sw) => (
          <li key={sw.id} className="flex items-baseline gap-4 px-4 py-1.5 text-sm">
            <span className="w-36 shrink-0 text-muted-foreground tabular-nums">
              {formatDateTime(sw.at)}
            </span>
            <span className="shrink-0 font-medium">{what(sw, walletName)}</span>
            <span className="min-w-0 truncate text-muted-foreground" title={sw.reason}>
              {who(sw)}
              {sw.reason &&
                (sw.polythought ? (
                  `: ${sw.reason}`
                ) : (
                  // An agent's or a token's own words, as it gave them.
                  <span className="italic">: “{sw.reason}”</span>
                ))}
            </span>
          </li>
        ))}
      </ul>

      {history.hasNextPage && (
        <div className="p-2 text-center">
          <Button
            variant="ghost"
            size="sm"
            disabled={history.isFetchingNextPage}
            onClick={() => void history.fetchNextPage()}
          >
            {history.isFetchingNextPage ? 'Loading…' : 'Show older'}
          </Button>
        </div>
      )}
    </>
  )
}

function what(
  sw: WorkflowSwitch,
  walletName: (w: NonNullable<WorkflowSwitch['wallet']>) => string,
) {
  const from = sw.wallet ? walletName(sw.wallet) : 'a wallet'

  switch (sw.kind) {
    case 'on':
      return 'Switched on'
    case 'off':
      return 'Switched off'
    case 'paper':
      return 'Back to paper'
    case 'live':
      return `Live, from ${from}`
    case 'wallet':
      return `Live from another wallet, ${from}`
    case 'reset':
      return 'Paper account reset'
    case 'kill_on':
      return 'Kill switch on'
    case 'kill_off':
      return 'Kill switch off'
  }
}

function who(sw: WorkflowSwitch): string {
  if (sw.polythought) return 'by Polythought'
  if (!sw.by) return 'by you'

  return `by ${actorName(sw.by)}${sw.by.confirmed ? ', confirmed by you' : ''}`
}
