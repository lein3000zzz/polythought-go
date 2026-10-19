import { LoaderCircle } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { errorMessage, type Schemas } from '@/api/client'
import {
  useApprovals,
  useSetUpApprovals,
  useSignApprovalSetup,
  useStartApprovalSetup,
} from '@/api/wallets'
import { ConfirmDialog } from '@/components/ConfirmDialog'
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
  signMessage,
  signTypedData,
  switchToPolygon,
} from '@/lib/ethereum'
import { formatDateTime, shortAddress } from '@/lib/format'

import { SigningProgress } from './signing'
import { useSigningFlow } from './useSigningFlow'

type Wallet = Schemas['Wallet']
type ApprovalSetup = Schemas['ApprovalSetup']

function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`
}

function sentence(text: string) {
  return `${text.charAt(0).toUpperCase()}${text.slice(1)}.`
}

/** How the last setup went, on the card. */
function SetupLine({ setup, ready }: { setup: ApprovalSetup; ready: boolean }) {
  switch (setup.state) {
    case 'submitted':
      return (
        <p className="flex items-center gap-1 text-muted-foreground">
          <LoaderCircle className="size-4 animate-spin" />
          Granting {plural(setup.granting, 'approval', 'approvals')} on Polygon, in a minute or so.
        </p>
      )
    case 'signing':
      return (
        <p className="text-muted-foreground">
          A setup waits for your signature: grant them again to finish.
        </p>
      )
    case 'failed':
      return (
        <p className="text-destructive">
          Granting the approvals failed: {setup.error || 'no reason given'}.
        </p>
      )
    default:
      return ready || setup.granting === 0 ? null : (
        <p className="text-muted-foreground">
          Granted: Polymarket&apos;s index shows them within a minute or so.
        </p>
      )
  }
}

/**
 * A wallet's trading approvals on its card (D160): granted, or which are missing and how
 * to grant them here, signed with the wallet's imported key or in its owner's browser
 * wallet (D161), or why they cannot be (D162).
 */
export function WalletApprovals({ wallet }: { wallet: Wallet }) {
  const approvals = useApprovals(wallet.id)
  const setUp = useSetUpApprovals(wallet.id)
  const [showing, setShowing] = useState(false)
  const [granting, setGranting] = useState(false)
  const [confirming, setConfirming] = useState(false)

  if (approvals.isPending) return null

  if (approvals.isError) {
    return (
      <p className="text-muted-foreground">
        Its trading approvals could not be read: {errorMessage(approvals.error)}
      </p>
    )
  }

  const { ready, missing, setup, way, why, checkedAt } = approvals.data
  const busy = setup?.state === 'submitted'

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
        {ready ? (
          <Badge variant="secondary">Approvals granted</Badge>
        ) : (
          <Badge variant="outline">{plural(missing.length, 'approval', 'approvals')} missing</Badge>
        )}
        <span className="text-muted-foreground">
          for trading, as Polymarket saw them {formatDateTime(checkedAt)}
        </span>
        {!ready && (
          <Button
            variant="link"
            size="sm"
            className="h-auto p-0"
            onClick={() => setShowing(!showing)}
          >
            {showing ? 'Hide them' : 'Show them'}
          </Button>
        )}
      </div>

      {showing && !ready && (
        <ul className="list-disc pl-5 text-muted-foreground">
          {missing.map((a) => (
            <li key={`${a.token}-${a.spender}`}>{a.name}</li>
          ))}
        </ul>
      )}

      {setup && <SetupLine setup={setup} ready={ready} />}

      {!ready && way === 'none' && why && <p className="text-muted-foreground">{sentence(why)}</p>}

      {!ready && way !== 'none' && (
        <div>
          <Button
            variant="outline"
            size="sm"
            disabled={busy || setUp.isPending}
            onClick={() => (way === 'key' ? setConfirming(true) : setGranting(true))}
          >
            Grant approvals
          </Button>
        </div>
      )}

      <ConfirmDialog
        open={confirming}
        title="Grant the approvals with the imported key?"
        description={`Polythought signs one transaction with ${wallet.label || shortAddress(wallet.address)}'s key, granting the ${plural(missing.length, 'approval', 'approvals')} Polymarket's exchanges need to trade for it. Polymarket's Relayer submits it and pays the gas.`}
        confirm="Grant"
        onCancel={() => setConfirming(false)}
        onConfirm={() =>
          setUp.mutate(undefined, {
            onSuccess: (s) =>
              s.granting === 0
                ? toast.success('Nothing to grant: the wallet has them all')
                : toast.success(
                    `Granting ${plural(s.granting, 'approval', 'approvals')} on Polygon`,
                  ),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }
      />

      <GrantApprovalsDialog
        wallet={wallet}
        missing={missing.length}
        open={granting}
        onClose={() => setGranting(false)}
      />
    </div>
  )
}

/**
 * Grants a smart wallet's missing approvals with its owner's browser wallet (D161): it
 * connects, switches to Polygon, and signs what the server gives, typed data for a
 * Deposit Wallet, a message for a Safe; the Relayer submits it and pays the gas.
 */
function GrantApprovalsDialog({
  wallet,
  missing,
  open,
  onClose,
}: {
  wallet: Wallet
  missing: number
  open: boolean
  onClose: () => void
}) {
  const flow = useSigningFlow(open)
  const start = useStartApprovalSetup(wallet.id)
  const sign = useSignApprovalSetup(wallet.id)
  const name = wallet.label || shortAddress(wallet.address)

  const close = () => {
    flow.reset()
    onClose()
  }

  const grant = async (signer: BrowserWallet) => {
    flow.setError(null)

    try {
      flow.setStep('Connecting…')
      const owner = await connect(signer.provider)

      flow.setStep('Switching to Polygon…')
      await switchToPolygon(signer.provider)

      flow.setStep('Preparing…')
      const setup = await start.mutateAsync(owner)

      if (!setup.typedData && !setup.message) {
        toast.success('Nothing to grant: the wallet has them all')
        close()
        return
      }

      flow.setStep(`Sign in ${signer.name}…`)
      const signature = setup.typedData
        ? await signTypedData(signer.provider, owner, setup.typedData)
        : await signMessage(signer.provider, owner, setup.message ?? '')

      flow.setStep('Submitting…')
      await sign.mutateAsync(signature)

      toast.success(
        `Signed: ${plural(setup.setup.granting, 'approval is', 'approvals are')} being granted on Polygon`,
      )
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
          <DialogTitle>Grant the trading approvals of {name}</DialogTitle>
          <DialogDescription>
            Polymarket&apos;s exchanges need the wallet&apos;s leave to move its pUSD and outcome
            tokens when it trades: {plural(missing, 'approval is', 'approvals are')} missing. Sign
            once, in the browser wallet of the account that owns this Deposit Wallet or Safe;
            Polymarket&apos;s Relayer submits it and pays the gas.
          </DialogDescription>
        </DialogHeader>

        <SigningProgress id={`approving-wallet-${wallet.id}`} flow={flow} />

        <DialogFooter>
          <Button variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button
            disabled={!flow.wallet || flow.step !== null}
            onClick={() => flow.wallet && void grant(flow.wallet)}
          >
            Connect and sign
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
