import { CircleAlert, Send } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { errorMessage, type Schemas } from '@/api/client'
import { useCreateTelegramLink, useTelegram, useUnlinkTelegram } from '@/api/telegram'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { configPath } from '@/features/config/pages'
import { formatDateTime } from '@/lib/format'

import { jumpId } from './pages'

export function TelegramSection() {
  const { data: me } = useMe()
  const [link, setLink] = useState<Schemas['TelegramLinkURL']>()
  const telegram = useTelegram(link?.expiresAt)
  const create = useCreateTelegramLink()
  const unlink = useUnlinkTelegram()
  const [confirmUnlink, setConfirmUnlink] = useState(false)

  const linked = telegram.data?.link
  const admin = me?.role === 'admin'

  return (
    <section id={jumpId('telegram')} className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">Telegram</h2>
        <p className="text-sm text-muted-foreground">
          Link Telegram to get messages from Polythought there.
        </p>
      </div>

      {telegram.isPending && <Skeleton className="h-12" />}
      {telegram.isError && (
        <ErrorAlert error={telegram.error} onRetry={() => void telegram.refetch()} />
      )}

      {telegram.data && !telegram.data.available && !linked && (
        <p className="text-sm text-muted-foreground">
          Telegram is not set up on this instance.
          {admin && (
            <>
              {' '}
              Turn on the bot and add its token under{' '}
              <Link
                to={configPath('telegram', 'telegram.enabled')}
                className="text-foreground underline underline-offset-4"
              >
                Admin → Config
              </Link>
              .
            </>
          )}
        </p>
      )}

      {admin && telegram.data?.problem && (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertTitle>The bot reported a problem</AlertTitle>
          <AlertDescription>
            {telegram.data.problem}
            {telegram.data.problemAt && ` (${formatDateTime(telegram.data.problemAt)})`}
          </AlertDescription>
        </Alert>
      )}

      {linked && (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4">
          <div className="flex items-center gap-2 text-sm">
            <Send className="size-4 text-muted-foreground" />
            <span>
              Linked to{' '}
              <span className="font-medium">
                {linked.username ? `@${linked.username}` : linked.firstName || 'your Telegram'}
              </span>{' '}
              <span className="text-muted-foreground">since {formatDateTime(linked.linkedAt)}</span>
            </span>
          </div>
          <Button variant="outline" size="sm" onClick={() => setConfirmUnlink(true)}>
            Unlink
          </Button>
        </div>
      )}

      {telegram.data?.available && !linked && !link && (
        <div className="flex flex-col items-start gap-2">
          {create.isError && <ErrorAlert error={create.error} />}
          <Button
            variant="outline"
            disabled={create.isPending}
            onClick={() => create.mutate(undefined, { onSuccess: setLink })}
          >
            <Send />
            Link Telegram
          </Button>
        </div>
      )}

      {telegram.data?.available && !linked && link && (
        <Alert>
          <Send />
          <AlertTitle>Open the link in Telegram and press Start</AlertTitle>
          <AlertDescription className="flex flex-col gap-2">
            <p>
              It links the Telegram account that opens it, once, until{' '}
              {formatDateTime(link.expiresAt)}. This page notices when it is done.
            </p>
            <div className="flex gap-2">
              <Button size="sm" asChild>
                <a href={link.url} target="_blank" rel="noopener">
                  Open Telegram
                </a>
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setLink(undefined)}>
                Cancel
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      )}

      <ConfirmDialog
        open={confirmUnlink}
        title="Unlink Telegram?"
        description="Polythought stops writing to that chat. You can link it again any time."
        confirm="Unlink"
        destructive
        onCancel={() => setConfirmUnlink(false)}
        onConfirm={() => {
          setConfirmUnlink(false)
          setLink(undefined)
          unlink.mutate(undefined, {
            onSuccess: () => toast.success('Telegram unlinked'),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }}
      />
    </section>
  )
}
