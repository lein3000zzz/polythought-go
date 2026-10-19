import { afterEach } from 'vitest'

// A browser wallet for tests: announced by EIP-6963, answering what the app asks of it.

type Call = { method: string; params?: unknown[] }

/** A browser wallet announcing itself (EIP-6963), on account, answering as told. */
export function browserWallet(account: string, answers: Record<string, () => unknown> = {}) {
  const calls: Call[] = []
  const provider = {
    request: async ({ method, params }: Call) => {
      calls.push({ method, params })
      if (answers[method]) return answers[method]()
      if (method === 'eth_requestAccounts') return [account]
      if (method === 'eth_signTypedData_v4') return '0x5ign'
      return null
    },
  }

  const announce = () =>
    window.dispatchEvent(
      new CustomEvent('eip6963:announceProvider', {
        detail: {
          info: { uuid: 'fake', name: 'Fake Wallet', icon: '', rdns: 'test.fake' },
          provider,
        },
      }),
    )
  window.addEventListener('eip6963:requestProvider', announce)
  cleanups.push(() => window.removeEventListener('eip6963:requestProvider', announce))

  return calls
}

const cleanups: (() => void)[] = []

afterEach(() => {
  cleanups.splice(0).forEach((f) => f())
})

/** A wallet answer: the user said no. */
export const declined = () => {
  throw { code: 4001, message: 'User rejected the request.' }
}
