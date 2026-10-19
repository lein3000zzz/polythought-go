import { useEffect, useState } from 'react'

import { type BrowserWallet, findWallets } from '@/lib/ethereum'

/**
 * The browser wallets in the page, looked for each time open turns true; null while
 * looking. forget makes the next look start afresh.
 */
function useBrowserWallets(open: boolean) {
  const [wallets, setWallets] = useState<BrowserWallet[] | null>(null)

  useEffect(() => {
    if (!open) return

    let live = true
    void findWallets().then((found) => live && setWallets(found))

    return () => {
      live = false
    }
  }, [open])

  return { wallets, forget: () => setWallets(null) }
}

/** The browser wallet chosen of those found, and the error and step of what it does. */
export function useSigningFlow(open: boolean) {
  const { wallets, forget } = useBrowserWallets(open)
  const [chosen, setChosen] = useState('')
  const [step, setStep] = useState<string | null>(null)
  const [error, setError] = useState<unknown>(null)

  const wallet = wallets?.find((w) => w.id === chosen) ?? wallets?.[0]

  const reset = () => {
    forget()
    setStep(null)
    setError(null)
  }

  return { wallets, wallet, chosen, setChosen, step, setStep, error, setError, reset }
}

export type SigningFlow = ReturnType<typeof useSigningFlow>
