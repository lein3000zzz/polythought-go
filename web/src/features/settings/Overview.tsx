import { useMe } from '@/api/auth'
import { useTokens } from '@/api/account'
import { useNotifications } from '@/api/notifications'
import { tokens, useAgentSpend } from '@/api/spend'
import { useTelegram } from '@/api/telegram'
import { useCredentialList } from '@/api/vault'
import { PageCard } from '@/components/PageCard'

import { settingsPath, type settingsPages } from './pages'

/** /settings itself (D392): a card for each page, saying how things are there. */
export function Overview({ pages }: { pages: typeof settingsPages }) {
  const me = useMe()
  const telegram = useTelegram()
  const notifications = useNotifications()
  const spend = useAgentSpend()
  const tokenList = useTokens()
  const credentials = useCredentialList()

  const count = (n: number, one: string, many: string) =>
    n === 0 ? 'None yet' : `${n} ${n === 1 ? one : many}`

  const t = telegram.data
  const kinds = notifications.data?.notifications
  const said: Record<string, string | undefined> = {
    account: me.data ? `Signed in as ${me.data.email}` : undefined,
    telegram:
      t &&
      (t.link
        ? `Linked${t.link.username ? ` as @${t.link.username}` : ''}`
        : t.available
          ? 'Not linked'
          : 'Not set up on this instance'),
    notifications:
      kinds &&
      `${kinds.filter((n) => n.enabled).length} on Telegram, ${kinds.filter((n) => n.email).length} by email, of ${kinds.length}`,
    agents:
      spend.data && `$${spend.data.dollars} this month · ${tokens(spend.data.totalTokens)} tokens`,
    tokens: tokenList.data && count(tokenList.data.length, 'token', 'tokens'),
    credentials: credentials.data && count(credentials.data.length, 'credential', 'credentials'),
  }

  return (
    <ul aria-label="Overview" className="grid gap-3 sm:grid-cols-2">
      {pages.map((p) => (
        <li key={p.slug}>
          <PageCard to={settingsPath(p.slug)} label={p.label}>
            {said[p.slug] ?? '…'}
          </PageCard>
        </li>
      ))}
    </ul>
  )
}
