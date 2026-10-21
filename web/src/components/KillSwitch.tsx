import { OctagonX } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { errorMessage, type Schemas } from '@/api/client'
import { useKillSwitch } from '@/api/trading'
import { useWallets } from '@/api/wallets'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'

type User = Schemas['User']

/**
 * Stops live trading at once (D225): for users with a wallet that can trade, while their
 * kill switch is off. Their workflows' live orders are refused and those resting on the
 * book cancelled.
 */
export function StopLiveButton({ user }: { user: User }) {
  const wallets = useWallets()
  const killSwitch = useKillSwitch()

  if (user.liveStopped || !wallets.data?.some((w) => w.hasKey || w.hasSessionKey)) return null

  const stop = () =>
    killSwitch.mutate(true, {
      onSuccess: (ks) => {
        toast.success(
          ks.cancelled > 0
            ? `Live trading stopped: ${ks.cancelled} resting ${ks.cancelled === 1 ? 'order' : 'orders'} cancelled`
            : 'Live trading stopped',
        )
        if (ks.left > 0) {
          toast.warning(
            `${ks.left} still ${ks.left === 1 ? 'rests' : 'rest'} on the book: Polymarket did not cancel ${ks.left === 1 ? 'it' : 'them'}. See their workflows' Trading view.`,
          )
        }
      },
      onError: (error) => toast.error(errorMessage(error)),
    })

  return (
    <Button variant="destructive" size="sm" onClick={stop} disabled={killSwitch.isPending}>
      <OctagonX />
      Stop live trading
    </Button>
  )
}

/** Says on every page that live trading is stopped, and resumes it once asked (D225). */
export function StoppedBanner({ user }: { user: User }) {
  const killSwitch = useKillSwitch()
  const [asking, setAsking] = useState(false)

  if (!user.liveStopped) return null

  return (
    <div
      role="status"
      className="flex shrink-0 items-center gap-3 border-b border-destructive/40 bg-destructive/10 px-4 py-2 text-sm text-destructive"
    >
      <OctagonX className="size-4 shrink-0" />
      <span className="font-medium">Live trading is stopped:</span>
      <span>
        your workflows place no live orders
        {user.killStopsAgents && ', and your AI agent nodes make no calls,'} until you resume it.
      </span>
      <Button
        variant="outline"
        size="sm"
        className="ml-auto"
        onClick={() => setAsking(true)}
        disabled={killSwitch.isPending}
      >
        Resume live trading
      </Button>

      <ConfirmDialog
        open={asking}
        title="Resume live trading?"
        description="Your workflows trading live place orders again, as before. Orders cancelled when you stopped stay cancelled."
        confirm="Resume"
        onCancel={() => setAsking(false)}
        onConfirm={() =>
          killSwitch.mutate(false, {
            onSuccess: () => {
              setAsking(false)
              toast.success('Live trading resumed')
            },
            onError: (error) => {
              setAsking(false)
              toast.error(errorMessage(error))
            },
          })
        }
      />
    </div>
  )
}
