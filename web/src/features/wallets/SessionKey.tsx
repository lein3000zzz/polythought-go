import { LoaderCircle } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import type { Schemas } from '@/api/client'
import {
  useRemoveSessionKey,
  useRevokeSessionKey,
  useSignSessionKey,
  useStartRevocation,
  useStartSessionKey,
} from '@/api/wallets'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  type BrowserWallet,
  connect,
  declined,
  sameAddress,
  signTypedData,
  switchToPolygon,
} from '@/lib/ethereum'
import { formatDateTime, shortAddress } from '@/lib/format'

import { SigningProgress } from './signing'
import { useSigningFlow } from './useSigningFlow'

type Wallet = Schemas['Wallet']
type SessionKey = Schemas['SessionKey']

const pendingText: Record<SessionKey['state'], string> = {
  signing: 'A session key waits for your signature: authorise it again to finish.',
  authorizing: 'A session key is being authorised on Polygon, in a minute or so.',
  active: '',
  failed: 'Authorising a session key failed',
}

/** What a wallet's session keys are doing, on its card. */
export function SessionKeyStatus({ keys }: { keys: Schemas['SessionKeys'] }) {
  const { active, pending } = keys
  const [now] = useState(() => Date.now()) // when the card showed, to tell an expired key
  const expired = active && Date.parse(active.validUntil) < now

  return (
    <>
      {active && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <Badge variant={expired ? 'destructive' : 'secondary'}>
            {expired ? 'Session key expired' : 'Session key'}
          </Badge>
          <span className="text-muted-foreground">
            <span className="font-mono">{shortAddress(active.signer)}</span>, places and cancels
            orders · {expired ? 'expired' : 'until'} {formatDateTime(active.validUntil)} · owner{' '}
            <span className="font-mono">{shortAddress(active.owner)}</span>
          </span>
        </div>
      )}
      {pending && (
        <p
          className={
            pending.state === 'failed'
              ? 'text-destructive'
              : 'flex items-center gap-1 text-muted-foreground'
          }
        >
          {pending.state === 'authorizing' && <LoaderCircle className="size-4 animate-spin" />}
          {pendingText[pending.state]}
          {pending.state === 'failed' && `: ${pending.error || 'no reason given'}.`}
        </p>
      )}
    </>
  )
}

/**
 * Authorises a session key for a Deposit Wallet (D156): the owner's browser wallet
 * connects, switches to Polygon, and signs the batch the server gives; the server submits
 * it to Polymarket's Relayer. Renewing makes a new key; the current one trades until the
 * new one is confirmed.
 */
export function AuthoriseSessionKeyDialog({
  wallet,
  renewing,
  open,
  onClose,
}: {
  wallet: Wallet
  renewing: boolean
  open: boolean
  onClose: () => void
}) {
  const flow = useSigningFlow(open)
  const start = useStartSessionKey(wallet.id)
  const sign = useSignSessionKey(wallet.id)
  const name = wallet.label || shortAddress(wallet.address)

  const close = () => {
    flow.reset()
    onClose()
  }

  const authorise = async (signer: BrowserWallet) => {
    flow.setError(null)

    try {
      flow.setStep('Connecting…')
      const owner = await connect(signer.provider)

      flow.setStep('Switching to Polygon…')
      await switchToPolygon(signer.provider)

      flow.setStep('Preparing…')
      const batch = await start.mutateAsync(owner)

      flow.setStep(`Sign in ${signer.name}…`)
      const signature = await signTypedData(signer.provider, owner, batch.typedData)

      flow.setStep('Submitting…')
      await sign.mutateAsync(signature)

      toast.success('Signed: the session key is being authorised on Polygon')
      close()
    } catch (error) {
      flow.setError(error)
      flow.setStep(null)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && close()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {renewing ? `Renew the session key of ${name}` : `Authorise a session key for ${name}`}
          </DialogTitle>
          <DialogDescription>
            A session key lets Polythought place and cancel this Deposit Wallet&apos;s orders, and
            nothing else, for 180 days. Polythought makes the key; you sign once, in the browser
            wallet of the account that owns the Deposit Wallet. Your own key never leaves your
            wallet.
            {renewing && ' The current session key trades until the new one is authorised.'}
          </DialogDescription>
        </DialogHeader>

        <SigningProgress id={`signing-wallet-${wallet.id}`} flow={flow} />

        <DialogFooter>
          <Button variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button
            disabled={!flow.wallet || flow.step !== null}
            onClick={() => flow.wallet && void authorise(flow.wallet)}
          >
            Connect and sign
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Removes a session key (D159): the owner signs its revocation, and Polymarket then cancels
 * its open orders. It is removed here either way: also when the owner declines to sign,
 * or chooses not to.
 */
export function RemoveSessionKeyDialog({
  wallet,
  sessionKey,
  open,
  onClose,
}: {
  wallet: Wallet
  sessionKey: SessionKey
  open: boolean
  onClose: () => void
}) {
  const flow = useSigningFlow(open)
  const startRevocation = useStartRevocation(wallet.id)
  const revoke = useRevokeSessionKey(wallet.id)
  const remove = useRemoveSessionKey(wallet.id)

  const close = () => {
    flow.reset()
    onClose()
  }

  const removeUnrevoked = async (why: string) => {
    try {
      flow.setStep('Removing…')
      await remove.mutateAsync()
      toast.warning(`Session key removed, not revoked: ${why}`)
      close()
    } catch (error) {
      flow.setError(error)
      flow.setStep(null)
    }
  }

  const revokeAndRemove = async (signer: BrowserWallet) => {
    flow.setError(null)

    let signature: string

    try {
      flow.setStep('Connecting…')
      const account = await connect(signer.provider)
      if (!sameAddress(account, sessionKey.owner)) {
        throw new Error(
          `${signer.name} is on ${shortAddress(account)}: switch it to ${shortAddress(sessionKey.owner)}, the owner, and try again.`,
        )
      }

      flow.setStep('Switching to Polygon…')
      await switchToPolygon(signer.provider)

      flow.setStep('Preparing…')
      const batch = await startRevocation.mutateAsync()

      flow.setStep(`Sign in ${signer.name}…`)
      signature = await signTypedData(signer.provider, account, batch.typedData)
    } catch (error) {
      if (declined(error)) {
        await removeUnrevoked('you declined to sign. Nobody can use it; it expires on its own.')
        return
      }

      flow.setError(error)
      flow.setStep(null)
      return
    }

    try {
      flow.setStep('Revoking…')
      const result = await revoke.mutateAsync(signature)

      if (result.revoked) {
        toast.success('Session key revoked and removed')
      } else {
        toast.warning(`Session key removed, not revoked: ${result.problem ?? 'no reason given'}`)
      }

      close()
    } catch (error) {
      flow.setError(error)
      flow.setStep(null)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && close()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove the session key?</DialogTitle>
          <DialogDescription>
            Sign once more, as {shortAddress(sessionKey.owner)}, to revoke it: Polymarket then
            cancels its open orders. The wallet can no longer trade until you authorise a new one.
            If you cannot sign, remove it without revoking: nobody can use it then, and it expires
            on {formatDateTime(sessionKey.validUntil)}.
          </DialogDescription>
        </DialogHeader>

        <SigningProgress id={`revoking-wallet-${wallet.id}`} flow={flow} />

        <DialogFooter>
          <Button variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button
            variant="ghost"
            className="text-destructive"
            disabled={flow.step !== null}
            onClick={() => void removeUnrevoked('you chose not to sign.')}
          >
            Remove without revoking
          </Button>
          <Button
            variant="destructive"
            disabled={!flow.wallet || flow.step !== null}
            onClick={() => flow.wallet && void revokeAndRemove(flow.wallet)}
          >
            Revoke and remove
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
