import { Navigate, useParams } from 'react-router'

import { useTokens } from '@/api/account'
import { useMe } from '@/api/auth'
import { ApiError } from '@/api/client'
import { useNotifications } from '@/api/notifications'
import { useGrants } from '@/api/oauth'
import { useAgentSpend } from '@/api/spend'
import { useCredentialList, useCredentialTypes } from '@/api/vault'
import { SideMenu } from '@/components/SideMenu'
import { AgentsSection } from '@/features/settings/AgentsSection'
import { ConnectedSection } from '@/features/settings/ConnectedSection'
import { Credentials } from '@/features/settings/Credentials'
import { NotificationsSection } from '@/features/settings/NotificationsSection'
import { Overview } from '@/features/settings/Overview'
import {
  jumpPrefix,
  matching,
  settingsPages,
  settingsPath,
  type Entry,
} from '@/features/settings/pages'
import { PasswordSection } from '@/features/settings/PasswordSection'
import { TelegramSection } from '@/features/settings/TelegramSection'
import { TokensSection } from '@/features/settings/TokensSection'
import { useJump } from '@/lib/jump'

/**
 * The account's Settings in pages (D379, D392), as Config's: an overview, a menu beside
 * the page, and a search for what is on them. Each page saves as it goes.
 */
export function SettingsPage() {
  const { page: slug = '' } = useParams()
  const spend = useAgentSpend()
  const notifications = useNotifications()
  const tokenList = useTokens()
  const credentials = useCredentialList()
  const types = useCredentialTypes()
  const grants = useGrants()

  // Without agents on the instance, there is no AI agents page.
  const noAgents = spend.error instanceof ApiError && spend.error.status === 404
  const pages = settingsPages.filter((p) => p.slug !== 'agents' || !noAgents)

  const typeLabel = new Map(types.data?.map((t) => [t.name, t.label]))
  const entries: Entry[] = [
    { slug: 'account', key: 'password', label: 'Password', words: 'change password account' },
    {
      slug: 'telegram',
      key: 'telegram',
      label: 'Link Telegram',
      words: 'telegram chat bot link unlink messages',
    },
    ...(notifications.data?.notifications ?? []).map((n) => ({
      slug: 'notifications',
      key: n.key,
      label: n.label,
      words: `${n.group} ${n.description} notifications`,
    })),
    ...(noAgents
      ? []
      : [
          {
            slug: 'agents',
            key: 'spend',
            label: 'Spend this month',
            words: 'ai agents spend dollars tokens caps month',
          },
          {
            slug: 'agents',
            key: 'kill-switch',
            label: 'Kill switch stops my agents',
            words: 'kill switch stop ai agents live trading',
          },
        ]),
    {
      slug: 'tokens',
      key: 'new-token',
      label: 'New API token',
      words: 'create api token bearer scripts',
    },
    ...(tokenList.data ?? []).map((t) => ({
      slug: 'tokens',
      key: `token-${t.id}`,
      label: t.name,
      words: 'api token',
    })),
    {
      slug: 'connected',
      key: 'connected',
      label: 'Connected agents',
      words: 'mcp oauth claude code codex cursor agents access revoke',
    },
    {
      slug: 'connected',
      key: 'agents-see-personal',
      label: 'Agents and tokens see personal settings',
      words: 'agents api tokens mcp personal sensitive webhook url secret hidden',
    },
    {
      slug: 'connected',
      key: 'agents-may-delete',
      label: 'Agents and tokens may delete',
      words: 'agents api tokens mcp delete remove workflows custom nodes credentials',
    },
    {
      slug: 'connected',
      key: 'agents-may-share',
      label: 'Agents and tokens may share to the library',
      words: 'agents api tokens mcp share library publish',
    },
    {
      slug: 'connected',
      key: 'agents-use-credentials',
      label: 'Agents and tokens may use my credentials in workflows',
      words: 'agents api tokens mcp credentials keys secrets use test',
    },
    ...(grants.data ?? [])
      .filter((g) => !g.revokedAt)
      .map((g) => ({
        slug: 'connected',
        key: `grant-${g.id}`,
        label: g.client.name,
        words: 'connected agent mcp revoke',
      })),
    {
      slug: 'credentials',
      key: 'new-credential',
      label: 'New credential',
      words: 'add credential key secret header',
    },
    ...(credentials.data ?? []).map((c) => ({
      slug: 'credentials',
      key: `credential-${c.id}`,
      label: c.name,
      words: `credential ${typeLabel.get(c.type) ?? c.type}`,
    })),
  ]

  useJump(
    jumpPrefix,
    entries.filter((e) => e.slug === slug).map((e) => e.key),
  )

  const page = pages.find((p) => p.slug === slug)
  if (slug !== '' && !page) return <Navigate to={settingsPath()} replace />

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-8 p-6">
      <h1 className="font-heading text-xl font-semibold">Settings</h1>

      <div className="flex flex-col gap-6 md:flex-row md:items-start">
        <SideMenu
          label="Settings pages"
          pages={pages}
          current={slug}
          path={settingsPath}
          find={(query) => matching(entries, query, pages)}
        />

        <div className="flex min-w-0 flex-1 flex-col gap-8">
          {slug === 'account' ? (
            <Account />
          ) : slug === 'telegram' ? (
            <TelegramSection />
          ) : slug === 'notifications' ? (
            <NotificationsSection />
          ) : slug === 'agents' ? (
            <AgentsSection />
          ) : slug === 'tokens' ? (
            <TokensSection />
          ) : slug === 'connected' ? (
            <ConnectedSection />
          ) : slug === 'credentials' ? (
            <Credentials />
          ) : (
            <Overview pages={pages} />
          )}
        </div>
      </div>
    </div>
  )
}

function Account() {
  const me = useMe()

  return (
    <div className="flex flex-col gap-8">
      <section className="flex flex-col gap-1">
        <h2 className="font-medium">Account</h2>
        <p className="text-sm text-muted-foreground">
          Signed in as <span className="text-foreground">{me.data?.email}</span>.
        </p>
      </section>
      <PasswordSection />
    </div>
  )
}
