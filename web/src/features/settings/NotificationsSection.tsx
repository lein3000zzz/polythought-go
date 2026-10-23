import { Fragment } from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { errorMessage } from '@/api/client'
import { useNotifications, useSetNotification } from '@/api/notifications'
import { useTelegram } from '@/api/telegram'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'

import { jumpId, settingsPath } from './pages'

export function NotificationsSection() {
  const notifications = useNotifications()
  const telegram = useTelegram()
  const me = useMe()
  const set = useSetNotification()
  const mailOn = notifications.data?.mailOn

  const change = (key: string, body: { enabled?: boolean; email?: boolean }) =>
    set.mutate({ key, ...body }, { onError: (error) => toast.error(errorMessage(error)) })

  return (
    <section className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">Notifications</h2>
        <p className="text-sm text-muted-foreground">
          On Telegram, to your linked chat
          {telegram.data && !telegram.data.link && (
            <>
              {' '}
              (
              <Link
                to={settingsPath('telegram')}
                className="text-foreground underline underline-offset-4"
              >
                link Telegram
              </Link>{' '}
              to get them)
            </>
          )}
          ; by email, to {me.data?.email ?? 'your address'}.
          {notifications.data &&
            !mailOn &&
            ' Email needs an admin to allow it, and mail set up on the server.'}
        </p>
      </div>

      {notifications.isPending && <Skeleton className="h-12" />}
      {notifications.isError && (
        <ErrorAlert error={notifications.error} onRetry={() => void notifications.refetch()} />
      )}

      {notifications.data && notifications.data.notifications.length > 0 && (
        <div className="flex justify-end gap-6 text-xs text-muted-foreground" aria-hidden>
          <span className="w-9 text-center">Telegram</span>
          <span className="w-9 text-center">Email</span>
        </div>
      )}

      {notifications.data?.notifications.map((n, i, all) => (
        <Fragment key={n.key}>
          {/* The kinds come grouped: a heading where a group starts. */}
          {n.group !== all[i - 1]?.group && (
            <h3 className="pt-2 text-xs font-medium tracking-wide text-muted-foreground uppercase">
              {n.group}
            </h3>
          )}
          <div id={jumpId(n.key)} className="flex items-start justify-between gap-4">
            <div className="flex flex-col gap-0.5">
              <span className="text-sm font-medium">{n.label}</span>
              <p id={`notify-${n.key}-hint`} className="text-xs text-muted-foreground">
                {n.description}
              </p>
            </div>
            <div className="flex shrink-0 gap-6">
              <Switch
                aria-label={`${n.label}, on Telegram`}
                aria-describedby={`notify-${n.key}-hint`}
                checked={n.enabled}
                disabled={set.isPending}
                onCheckedChange={(enabled) => change(n.key, { enabled })}
              />
              <Switch
                aria-label={`${n.label}, by email`}
                aria-describedby={`notify-${n.key}-hint`}
                checked={n.email}
                disabled={set.isPending || !mailOn}
                onCheckedChange={(email) => change(n.key, { email })}
              />
            </div>
          </div>
        </Fragment>
      ))}
    </section>
  )
}
