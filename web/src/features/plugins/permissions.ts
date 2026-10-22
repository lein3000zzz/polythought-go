/** A permission a plugin asks for, in words for the admin who approves it (D331). */
export type Permission = {
  name: string
  title: string
  detail: string
  /** Called out on the permissions screen: orders, any address, and the owners' wallets. */
  risky: boolean
}

/** describe puts a permission in words; one the server does not know reads as it is. */
export function describe(name: string): Permission {
  if (name === 'fetch:*') {
    return {
      name,
      title: 'Reach any address on the internet',
      detail:
        'It may send requests to any public site and read the answers, anything its nodes are given among them. Never inside the server’s network, unless that is allowed on the Config page.',
      risky: true,
    }
  }

  const host = name.startsWith('fetch:') ? name.slice('fetch:'.length) : undefined
  if (host) {
    return {
      name,
      title: `Reach ${host}`,
      detail: `It may send requests to ${host} and read the answers, on any port.`,
      risky: false,
    }
  }

  switch (name) {
    case 'wallets':
      return {
        name,
        title: 'See the wallets of the people who use it',
        detail:
          'The names and addresses of the wallets of each workflow’s owner, which tie them to their money on chain.',
        risky: true,
      }
    case 'telegram':
      return {
        name,
        title: 'Message the people who use it on Telegram',
        detail:
          'It may write to each workflow’s owner in their linked chat, after its name, and ask them questions their workflows wait on.',
        risky: false,
      }
    case 'orders':
      return {
        name,
        title: 'Place and cancel orders',
        detail:
          'Its nodes marked as trading may place orders for the workflows that use them, and cancel their resting ones: on paper or live as each workflow trades, within the caps and the kill switch.',
        risky: true,
      }
    default:
      return { name, title: name, detail: 'A permission this page does not know.', risky: true }
  }
}

/** What every plugin may do, asking nothing: nothing of it needs approving. */
export const always = [
  'Read Polymarket’s public data: markets, order books, prices, trades, and any address’s positions.',
  'Keep data for its nodes that say they remember, shown as their memory.',
  'Read the credentials people pick in its nodes’ settings, and only those.',
]

type Named = { name?: string; label?: string; id?: string }

/** What a version brings, as its manifest says. */
export type Brings = {
  description: string
  nodes: string[]
  triggers: string[]
  credentialTypes: string[]
  scenarios: string[]
}

export function brings(manifest: unknown): Brings {
  const m = (typeof manifest === 'object' && manifest !== null ? manifest : {}) as Record<
    string,
    unknown
  >
  const names = (key: string) =>
    (Array.isArray(m[key]) ? (m[key] as Named[]) : []).map((n) => n.label || n.name || n.id || '')

  return {
    description: typeof m.description === 'string' ? m.description : '',
    nodes: names('nodes'),
    triggers: names('triggers'),
    credentialTypes: names('credentialTypes'),
    scenarios: names('scenarios'),
  }
}
