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
type ConfirmSwitchSpec = { key: keyof LiveConfirm; id: string; label: string; hint: string }

/** The live actions of your agents and tokens that wait for you, and those waiting (D406). */
const switches: ConfirmSwitchSpec[] = [
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

/** An admin's agents' and tokens' work on other users that waits for them (D410). */
const adminSwitches: ConfirmSwitchSpec[] = [
  {
    key: 'role',
    id: 'confirm-role',
    label: 'Changing a role',
    hint: 'Making a user an admin, or one no longer.',
  },
  {
    key: 'disable',
    id: 'confirm-disable',
    label: 'Disabling or enabling a user',
    hint: 'Disabling signs them out, stops their workflows and turns their kill switch on.',
  },
  {
    key: 'caps',
    id: 'confirm-caps',
    label: "Raising a user's caps",
    hint: 'Their own caps on live buys and on their agents’ spend, raised or removed; lowering them goes ahead.',
  },
  {
    key: 'code',
    id: 'confirm-code',
    label: 'Letting a user write code',
    hint: 'Where code is for chosen users.',
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
          While a switch is on, what an agent or an API token asks for waits here, with a bar on
          every page and a message on Telegram with its buttons, until you confirm it; then it is
          carried out as it asked. Your own actions never wait. Notifications says how you are told.
        </p>
      </div>

      {switches.map((s) => (
        <ConfirmSwitch key={s.key} spec={s} />
      ))}

      {me.data?.role === 'admin' && (
        <>
          <h4 className="text-sm font-medium">An admin&apos;s work on other users</h4>
          {adminSwitches.map((s) => (
            <ConfirmSwitch key={s.key} spec={s} />
          ))}
        </>
      )}

      <div id={jumpId('confirm-in-agent')} className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-0.5">
          <Label htmlFor="confirm-in-agent">Also ask in the agent&apos;s window</Label>
          <p id="confirm-in-agent-hint" className="text-xs text-muted-foreground">
            Where its client can ask: a link to this app, where you decide, or where it shows no
            links, a Confirm and a Refuse in the agent itself, whose answer counts. Its call waits a
            little for you, then the request waits here.
          </p>
        </div>
        <Switch
          id="confirm-in-agent"
          aria-describedby="confirm-in-agent-hint"
          checked={me.data?.liveConfirm.inAgent ?? false}
          disabled={set.isPending || !me.data}
          onCheckedChange={(on) =>
            set.mutate(
              { inAgent: on },
              {
                onSuccess: () =>
                  toast.success(
                    on
                      ? 'Agents ask you in their window too, where they can'
                      : 'Agents no longer ask in their window',
                  ),
                onError: (error) => toast.error(errorMessage(error)),
              },
            )
          }
        />
      </div>

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
                  {c.about && ` (${c.about})`}
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
                {c.about && ` (${c.about})`}
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

function ConfirmSwitch({ spec: s }: { spec: ConfirmSwitchSpec }) {
  const me = useMe()
  const set = useSetLiveConfirm()

  return (
    <div id={jumpId(s.id)} className="flex items-start justify-between gap-4">
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
  )
}
