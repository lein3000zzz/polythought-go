import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  connect,
  declined,
  type EthereumProvider,
  findWallets,
  sameAddress,
  signMessage,
  signTypedData,
  switchToPolygon,
  walletErrorMessage,
} from './ethereum'

function announcing(uuid: string, name: string, provider: EthereumProvider) {
  const announce = () =>
    window.dispatchEvent(
      new CustomEvent('eip6963:announceProvider', {
        detail: { info: { uuid, name, icon: '', rdns: 'test' }, provider },
      }),
    )
  window.addEventListener('eip6963:requestProvider', announce)

  return () => window.removeEventListener('eip6963:requestProvider', announce)
}

const provider = (request: EthereumProvider['request'] = vi.fn()): EthereumProvider => ({ request })

describe('browser wallets', () => {
  const cleanups: (() => void)[] = []

  afterEach(() => {
    cleanups.splice(0).forEach((f) => f())
    delete window.ethereum
  })

  it('finds those that announce themselves (EIP-6963)', async () => {
    cleanups.push(announcing('a', 'Rabby', provider()), announcing('b', 'MetaMask', provider()))
    window.ethereum = provider()

    const found = await findWallets(10)
    expect(found.map((w) => w.name)).toEqual(['Rabby', 'MetaMask'])
  })

  it('falls back on window.ethereum, or finds none', async () => {
    expect(await findWallets(10)).toEqual([])

    window.ethereum = provider()
    const found = await findWallets(10)
    expect(found).toEqual([
      { id: 'window.ethereum', name: 'Browser wallet', provider: window.ethereum },
    ])
  })

  it('connects, switches to Polygon and signs', async () => {
    const request = vi.fn(async ({ method }: { method: string }) => {
      if (method === 'eth_requestAccounts') return ['0xowner']
      if (method === 'eth_signTypedData_v4' || method === 'personal_sign') return '0x5ign'
      return null
    })
    const p = provider(request)

    expect(await connect(p)).toBe('0xowner')
    await switchToPolygon(p)
    expect(await signTypedData(p, '0xowner', { primaryType: 'Batch' })).toBe('0x5ign')
    expect(await signMessage(p, '0xowner', '0xabcd')).toBe('0x5ign')

    expect(request.mock.calls.map(([args]) => args)).toEqual([
      { method: 'eth_requestAccounts' },
      { method: 'wallet_switchEthereumChain', params: [{ chainId: '0x89' }] },
      { method: 'eth_signTypedData_v4', params: ['0xowner', '{"primaryType":"Batch"}'] },
      { method: 'personal_sign', params: ['0xabcd', '0xowner'] },
    ])
  })

  it('adds Polygon when the wallet does not know it', async () => {
    const request = vi.fn(async ({ method }: { method: string }) => {
      if (method === 'wallet_switchEthereumChain')
        throw Object.assign(new Error('unknown chain'), { code: 4902 })
      return null
    })

    await switchToPolygon(provider(request))
    expect(request).toHaveBeenLastCalledWith(
      expect.objectContaining({ method: 'wallet_addEthereumChain' }),
    )
  })

  it('a refusal to switch stays one', async () => {
    const request = vi.fn(async () => {
      throw { code: 4001, message: 'User rejected the request.' }
    })

    await expect(switchToPolygon(provider(request))).rejects.toMatchObject({ code: 4001 })
  })

  it('a wallet that gives no account', async () => {
    await expect(connect(provider(vi.fn(async () => [])))).rejects.toThrow(
      'The browser wallet gave no account.',
    )
  })

  it('says what went wrong', () => {
    expect(declined({ code: 4001 })).toBe(true)
    expect(declined(new Error('no'))).toBe(false)
    expect(walletErrorMessage({ code: 4001, message: 'User rejected' })).toBe(
      'You declined in your browser wallet.',
    )
    expect(walletErrorMessage({ code: -32603, message: 'Internal error' })).toBe('Internal error')
    expect(walletErrorMessage(new Error('Locked'))).toBe('Locked')
    expect(walletErrorMessage(42)).toBe('The browser wallet failed.')
    expect(sameAddress('0xAbC', '0xabc')).toBe(true)
  })
})
