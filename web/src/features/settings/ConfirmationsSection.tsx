import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { errorMessage, type Schemas } from '@/api/client'
import {
  type Confirmation,
  useConfirm,
  useConfirmations,
  useRefuse,
  useSetLiveConfirm,
} from '@/api/confirmations'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { formatDateTime } from '@/lib/format'

import { jumpId } from './pages'

type LiveConfirm = Schemas['LiveConfirm']

/** The live actions of your agents and tokens that wait for you, and those waiting (D406). */
const switches: { key: keyof LiveConfirm; id: string; label: string; hint: string }[] = [
  {
    key: 'goLive',
    id: 'confirm-go-live',
    label: 'Going live',
    hint: 'Setting a workflow to trade live, or changing the wallet it trades live from.',
  },
  {
    key: 'run',
    id: 'confirm-run',
    label: 'Switching on or running a live workflow',
    hint: 'Switching on, or running by hand, a workflow that trades live.',
  },
  {
    key: 'change',
    id: 'confirm-change',
    label: 'Changing a live workflow that is on',
    hint: 'Saving a new graph for a workflow that trades live and is switched on, which changes what it buys.',
  },
  {
    key: 'killOff',
    id: 'confirm-kill-off',
    label: 'Turning the kill switch off',
    hint: 'Which lets your workflows trade live again.',
  },
]

const statusLabel: Record<Confirmation['status'], string> = {
  pending: 'Waiting for you',
  confirmed: 'Confirmed, being carried out',
  done: 'Done',
  failed: 'Failed',
  refused: 'Refused',
  expired: 'Expired',
}

export function ConfirmationsSection() {
  const me = useMe()
  const set = useSetLiveConfirm()
  const list = useConfirmations()
  const confirm = useConfirm()
  const refuse = useRefuse()

  const pending = list.data?.filter((c) => c.status === 'pending') ?? []
  const ended = list.data?.filter((c) => c.status !== 'pending') ?? []

  return (
    <section id={jumpId('confirmations')} className="flex flex-col gap-4">
      <div>
        <h3 className="font-medium">Confirm my agents' live actions</h3>
        <p className="text-sm text-muted-foreground">
          While a switch is on, what an agent or an API token asks for waits here, and on every
          page, until you confirm it; then it is carried out as it asked. Your own actions never
          wait.
        </p>
      </div>

      {switches.map((s) => (
        <div key={s.key} id={jumpId(s.id)} className="flex items-start justify-between gap-4">
          <div className="flex flex-col gap-0.5">
            <Label htmlFor={s.id}>{s.label}</Label>
            <p id={`${s.id}-hint`} className="text-xs text-muted-foreground">
              {s.hint}
            </p>
          </div>
          <Switch
            id={s.id}
            aria-describedby={`${s.id}-hint`}
            checked={me.data?.liveConfirm[s.key] ?? true}
            disabled={set.isPending || !me.data}
            onCheckedChange={(on) =>
              set.mutate(
                { [s.key]: on },
                {
                  onSuccess: () =>
                    toast.success(
                      on ? `${s.label} waits for you` : `${s.label} no longer waits for you`,
                    ),
                  onError: (error) => toast.error(errorMessage(error)),
                },
              )
            }
          />
        </div>
      ))}

      {list.isPending && <Skeleton className="h-16" />}
      {list.isError && <ErrorAlert error={list.error} onRetry={() => void list.refetch()} />}

      {pending.length > 0 && (
        <ul aria-label="Waiting for you" className="flex flex-col gap-2">
          {pending.map((c) => (
            <li
              key={c.id}
              className="flex flex-wrap items-center gap-3 rounded-md border border-amber-500/40 bg-amber-500/5 p-3 text-sm"
            >
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span>
                  <span className="font-medium">{c.asker}</span> asks: {c.what}
                </span>
                <span className="text-xs text-muted-foreground">
                  Asked {formatDateTime(c.createdAt)}; waits until {formatDateTime(c.expiresAt)}
                </span>
              </div>
              <Button
                size="sm"
                disabled={confirm.isPending || refuse.isPending}
                onClick={() =>
                  confirm.mutate(c.id, {
                    onSuccess: () => toast.success('Confirmed: it is being carried out'),
                    onError: (error) => toast.error(errorMessage(error)),
                  })
                }
              >
                Confirm
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={confirm.isPending || refuse.isPending}
                onClick={() =>
                  refuse.mutate(c.id, {
                    onSuccess: () => toast.success('Refused'),
                    onError: (error) => toast.error(errorMessage(error)),
                  })
                }
              >
                Refuse
              </Button>
            </li>
          ))}
        </ul>
      )}

      {ended.length > 0 && (
        <ul aria-label="Asked before" className="flex flex-col gap-1 text-sm">
          {ended.slice(0, 10).map((c) => (
            <li key={c.id} className="flex flex-wrap items-center gap-2 text-muted-foreground">
              <Badge variant={c.status === 'failed' ? 'destructive' : 'outline'}>
                {statusLabel[c.status]}
              </Badge>
              <span>
                {c.asker}: {c.what}
                {c.result && ` (${c.result})`}
              </span>
              <span className="text-xs">{formatDateTime(c.createdAt)}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
