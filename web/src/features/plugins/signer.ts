import type { PluginVersion } from '@/api/plugins'

/** Who signed a version (D280): a key an admin trusts, by its name, or nobody they trust. */
export function signedBy(v: Pick<PluginVersion, 'signer' | 'keyId'>): {
  trusted: boolean
  text: string
} {
  if (v.signer) return { trusted: true, text: `Signed by ${v.signer}` }
  if (v.keyId) return { trusted: false, text: `Signed by a key you do not trust (${v.keyId})` }

  return { trusted: false, text: 'Not signed' }
}
