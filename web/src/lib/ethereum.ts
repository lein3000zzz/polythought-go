/**
 * Browser wallets (D157): those injected into the page, which announce themselves by
 * EIP-6963, or the one at window.ethereum. They sign what a session key's authorisation
 * or revocation needs, with eth_signTypedData_v4 on Polygon. No WalletConnect.
 */

/** An EIP-1193 provider: how the page talks to a browser wallet. */
export type EthereumProvider = {
  request(args: { method: string; params?: unknown[] }): Promise<unknown>
}

/** A browser wallet the page found. */
export type BrowserWallet = {
  id: string
  name: string
  icon?: string
  provider: EthereumProvider
}

declare global {
  interface Window {
    ethereum?: EthereumProvider
  }
}

type Announcement = {
  info: { uuid: string; name: string; icon?: string; rdns?: string }
  provider: EthereumProvider
}

/** Polygon's chain ID, hex as wallets take it. */
export const polygonChainId = '0x89'

/**
 * The browser wallets in the page: those that answer an EIP-6963 request within wait ms,
 * or else the one at window.ethereum. Empty without any.
 */
export async function findWallets(wait = 150): Promise<BrowserWallet[]> {
  const found = new Map<string, BrowserWallet>()

  const onAnnounce = (event: Event) => {
    const { info, provider } = (event as CustomEvent<Announcement>).detail
    found.set(info.uuid, { id: info.uuid, name: info.name, icon: info.icon, provider })
  }

  window.addEventListener('eip6963:announceProvider', onAnnounce)
  window.dispatchEvent(new Event('eip6963:requestProvider'))
  await new Promise((resolve) => setTimeout(resolve, wait))
  window.removeEventListener('eip6963:announceProvider', onAnnounce)

  if (found.size === 0 && window.ethereum) {
    return [{ id: 'window.ethereum', name: 'Browser wallet', provider: window.ethereum }]
  }

  return [...found.values()]
}

/** The wallet's account, once its user allows the page to see it. */
export async function connect(provider: EthereumProvider): Promise<string> {
  const accounts = (await provider.request({ method: 'eth_requestAccounts' })) as string[]
  if (!accounts?.[0]) throw new Error('The browser wallet gave no account.')

  return accounts[0]
}

/** Switches the wallet to Polygon, adding Polygon if the wallet does not know it. */
export async function switchToPolygon(provider: EthereumProvider): Promise<void> {
  try {
    await provider.request({
      method: 'wallet_switchEthereumChain',
      params: [{ chainId: polygonChainId }],
    })
  } catch (error) {
    if (errorCode(error) !== 4902) throw error

    await provider.request({
      method: 'wallet_addEthereumChain',
      params: [
        {
          chainId: polygonChainId,
          chainName: 'Polygon',
          nativeCurrency: { name: 'POL', symbol: 'POL', decimals: 18 },
          rpcUrls: ['https://polygon-rpc.com'],
          blockExplorerUrls: ['https://polygonscan.com'],
        },
      ],
    })
  }
}

/** Has the account sign typed data, EIP-712, with eth_signTypedData_v4. */
export async function signTypedData(
  provider: EthereumProvider,
  account: string,
  typedData: unknown,
): Promise<string> {
  return (await provider.request({
    method: 'eth_signTypedData_v4',
    params: [account, JSON.stringify(typedData)],
  })) as string
}

/** Whether the user said no in their wallet (EIP-1193 code 4001). */
export function declined(error: unknown): boolean {
  return errorCode(error) === 4001
}

function errorCode(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null || !('code' in error)) return undefined

  return Number(error.code)
}

/** A browser wallet's error for people: its message, or what it was. */
export function walletErrorMessage(error: unknown): string {
  if (declined(error)) return 'You declined in your browser wallet.'
  if (error instanceof Error) return error.message
  if (typeof error === 'object' && error !== null && 'message' in error)
    return String(error.message)

  return 'The browser wallet failed.'
}

/** Whether two addresses are the same, in any case. */
export function sameAddress(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase()
}
