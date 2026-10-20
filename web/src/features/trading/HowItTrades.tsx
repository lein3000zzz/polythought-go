import { useState } from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { errorMessage, type Schemas } from '@/api/client'
import { useSetTradingSetup, useTradingSetup, useWalletCash } from '@/api/trading'
import { useWallets } from '@/api/wallets'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { formatDollars, shortAddress } from '@/lib/format'

type Wallet = Schemas['Wallet']
type Setup = Schemas['TradingSetup']

function walletName(w: Wallet): string {
  return w.label || shortAddress(w.address)
}

function sentence(text: string): string {
  return `${text.charAt(0).toUpperCase()}${text.slice(1)}`
}

/** A wallet that can sign orders: its imported key, or a session key (D153). */
function canTrade(w: Wallet): boolean {
  return w.hasKey || w.hasSessionKey
}

/**
 * How a workflow trades (D191): on paper, or live from one of its owner's wallets, with
 * the switch between them. Going live asks first (D201); going back to paper does not.
 */
export function HowItTrades({ workflowId }: { workflowId: string }) {
  const setup = useTradingSetup(workflowId)
  const change = useSetTradingSetup(workflowId)
  const [asking, setAsking] = useState(false)

  if (!setup.data) return null

  const s = setup.data
  const live = s.mode === 'live'

  const toPaper = () =>
    change.mutate(
      { mode: 'paper', walletId: s.wallet?.id, confirm: false },
      {
        onSuccess: () => toast.success('Trading on paper'),
        onError: (error) => toast.error(errorMessage(error)),
      },
    )

  return (
    <section
      aria-label="How it trades"
      className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-lg border px-4 py-3 text-sm"
    >
      <Badge variant={live ? 'destructive' : 'secondary'}>{live ? 'Live' : 'Paper'}</Badge>
      <p className="flex flex-col gap-0.5">
        <span>
          {live
            ? s.wallet
              ? `Trades live from ${walletName(s.wallet)}: its orders spend real money.`
              : 'Trades live, but its wallet is gone: its orders are refused.'
            : 'Trades on paper: its orders are simulated against the live book.'}
        </span>
        {live && !s.liveAvailable && s.why && (
          <span className="text-destructive">
            {sentence(s.why)}: its orders are refused until then.
          </span>
        )}
        {!live && !s.liveAvailable && s.why && (
          <span className="text-muted-foreground">Live: {s.why}.</span>
        )}
      </p>
      <div className="ml-auto flex gap-2">
        {live ? (
          <Button variant="outline" size="sm" disabled={change.isPending} onClick={toPaper}>
            Back to paper
          </Button>
        ) : (
          <Button size="sm" disabled={!s.liveAvailable} onClick={() => setAsking(true)}>
            Trade live…
          </Button>
        )}
      </div>

      <GoLiveDialog
        key={String(asking)}
        workflowId={workflowId}
        setup={s}
        open={asking}
        onClose={() => setAsking(false)}
      />
    </section>
  )
}

/**
 * Asks before a workflow trades live (D201): from which wallet, what it holds in pUSD,
 * and that its orders now spend real money; a checkbox unlocks the switch.
 */
function GoLiveDialog({
  workflowId,
  setup,
  open,
  onClose,
}: {
  workflowId: string
  setup: Setup
  open: boolean
  onClose: () => void
}) {
  const wallets = useWallets()
  const change = useSetTradingSetup(workflowId)
  const [chosen, setChosen] = useState<string>()
  const [understood, setUnderstood] = useState(false)

  const trading = (wallets.data ?? []).filter(canTrade)
  // Holding live shares, the workflow keeps its wallet (D202).
  const walletId = setup.holdsLive
    ? setup.wallet?.id
    : (chosen ?? (setup.wallet && canTrade(setup.wallet) ? setup.wallet.id : trading[0]?.id))
  const wallet = trading.find((w) => w.id === walletId)
  const cash = useWalletCash(open ? wallet?.id : undefined)

  const goLive = () =>
    walletId &&
    change.mutate(
      { mode: 'live', walletId, confirm: true },
      {
        onSuccess: (s) => {
          toast.success(`Trading live from ${s.wallet ? walletName(s.wallet) : 'the wallet'}`)
          onClose()
        },
        onError: (error) => toast.error(errorMessage(error)),
      },
    )

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Trade live?</DialogTitle>
          <DialogDescription>
            Its orders go to Polymarket from your wallet and spend its pUSD, real money, within the
            Risk guard&apos;s caps. Polythought signs them with the wallet&apos;s key. Back to paper
            stops them at any time.
          </DialogDescription>
        </DialogHeader>

        {wallets.isPending ? null : trading.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            None of your wallets can trade yet: import a wallet&apos;s private key, or authorise a
            session key, on the <Link to="/wallets">Wallets</Link> page.
          </p>
        ) : (
          <div className="flex flex-col gap-3 text-sm">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="live-wallet">Wallet</Label>
              <NativeSelect
                id="live-wallet"
                value={walletId ?? ''}
                disabled={setup.holdsLive}
                onChange={(e) => setChosen(e.target.value)}
              >
                {trading.map((w) => (
                  <NativeSelectOption key={w.id} value={w.id}>
                    {walletName(w)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
              {setup.holdsLive && (
                <span className="text-xs text-muted-foreground">
                  It holds live shares from this wallet, so it stays until they are sold or settled.
                </span>
              )}
            </div>

            <p aria-live="polite">
              {cash.isPending
                ? 'Reading its pUSD…'
                : cash.isError
                  ? `Its pUSD could not be read: ${errorMessage(cash.error)}`
                  : `It holds ${formatDollars(cash.data.cash)} of pUSD.`}
            </p>

            <div className="flex items-center gap-2">
              <Checkbox
                id="live-understood"
                checked={understood}
                onCheckedChange={(checked) => setUnderstood(checked === true)}
              />
              <Label htmlFor="live-understood">I understand: its orders spend real money</Label>
            </div>
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={!understood || !wallet || change.isPending}
            onClick={goLive}
          >
            Switch to live
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
