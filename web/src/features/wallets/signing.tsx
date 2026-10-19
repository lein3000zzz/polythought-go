import { CircleAlert, LoaderCircle } from 'lucide-react'

import { ApiError, errorMessage } from '@/api/client'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { type BrowserWallet, walletErrorMessage } from '@/lib/ethereum'

import type { SigningFlow } from './useSigningFlow'

// What the dialogs signing with the owner's browser wallet share (D157): finding the
// wallet, choosing one, and showing how far they are and what went wrong.

/** Which browser wallet signs: a choice when there are several, a note when there is none. */
function WalletPicker({
  id,
  wallets,
  chosen,
  onChoose,
}: {
  id: string
  wallets: BrowserWallet[] | null
  chosen: string
  onChoose: (id: string) => void
}) {
  if (wallets === null) {
    return <p className="text-sm text-muted-foreground">Looking for browser wallets…</p>
  }

  if (wallets.length === 0) {
    return (
      <Alert>
        <CircleAlert />
        <AlertDescription>
          No browser wallet found. Install one, such as MetaMask or Rabby, with the account that
          owns this wallet, then open this again. Email and Google sign-ins of polymarket.com cannot
          sign here.
        </AlertDescription>
      </Alert>
    )
  }

  if (wallets.length === 1) {
    return <p className="text-sm text-muted-foreground">Signs with {wallets[0].name}.</p>
  }

  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>Browser wallet</Label>
      <NativeSelect
        id={id}
        className="w-full"
        value={chosen}
        onChange={(e) => onChoose(e.target.value)}
      >
        {wallets.map((w) => (
          <NativeSelectOption key={w.id} value={w.id}>
            {w.name}
          </NativeSelectOption>
        ))}
      </NativeSelect>
    </div>
  )
}

function FlowError({ error }: { error: unknown }) {
  return (
    <Alert variant="destructive">
      <CircleAlert />
      <AlertDescription>
        <p>{error instanceof ApiError ? errorMessage(error) : walletErrorMessage(error)}</p>
      </AlertDescription>
    </Alert>
  )
}

/** A signing dialog's body: what went wrong, which wallet signs, and the step it is at. */
export function SigningProgress({ id, flow }: { id: string; flow: SigningFlow }) {
  return (
    <div className="flex flex-col gap-3">
      {flow.error !== null && <FlowError error={flow.error} />}
      <WalletPicker
        id={id}
        wallets={flow.wallets}
        chosen={flow.wallet?.id ?? ''}
        onChoose={flow.setChosen}
      />
      {flow.step && (
        <p className="flex items-center gap-2 text-sm">
          <LoaderCircle className="size-4 animate-spin" />
          {flow.step}
        </p>
      )}
    </div>
  )
}
