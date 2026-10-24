import { Link, useParams } from 'react-router'
import { toast } from 'sonner'

import { errorMessage } from '@/api/client'
import { useConfirm, useConfirmations, useRefuse } from '@/api/confirmations'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { ConfirmationDetail } from '@/features/settings/ConfirmationDetail'
import { settingsPath } from '@/features/settings/pages'
import { formatDateTime } from '@/lib/format'

const ended: Record<string, string> = {
  confirmed: 'Confirmed: it is being carried out.',
  done: 'Confirmed, and done.',
  failed: 'Confirmed, but it failed.',
  refused: 'Refused.',
  expired: 'Not confirmed in time: it expired.',
}

/**
 * One live action waiting for you (D406), where an agent's window links (D409): confirm or
 * refuse it here, then go back to the agent, which carries on.
 */
export function ConfirmationPage() {
  const { id } = useParams()
  const list = useConfirmations()
  const confirm = useConfirm()
  const refuse = useRefuse()

  const c = list.data?.find((x) => x.id === id)
  const busy = confirm.isPending || refuse.isPending

  return (
    <div className="mx-auto flex max-w-xl flex-col gap-6 p-6">
      <h1 className="font-heading text-xl font-semibold">Confirm a live action</h1>

      {list.isPending && <Skeleton className="h-32" />}
      {list.isError && <ErrorAlert error={list.error} onRetry={() => void list.refetch()} />}
      {list.isSuccess && !c && (
        <p className="text-sm text-muted-foreground">
          There is no such request of yours. See{' '}
          <Link to={settingsPath('connected', 'confirmations')} className="underline">
            what your agents asked
          </Link>
          .
        </p>
      )}

      {c && (
        <section aria-label="The request" className="flex flex-col gap-4 rounded-lg border p-4">
          <p>
            <span className="font-medium">{c.asker}</span> asks you to confirm: {c.what}
            {c.about && ` (${c.about})`}.
          </p>
          <ConfirmationDetail detail={c.detail} open />
          <p className="text-xs text-muted-foreground">
            Asked {formatDateTime(c.createdAt)}; waits until {formatDateTime(c.expiresAt)}.
          </p>

          {c.status === 'pending' ? (
            <div className="flex gap-2">
              <Button
                disabled={busy}
                onClick={() =>
                  confirm.mutate(c.id, {
                    onSuccess: () => toast.success('Confirmed: you can go back to your agent'),
                    onError: (error) => toast.error(errorMessage(error)),
                  })
                }
              >
                Confirm
              </Button>
              <Button
                variant="outline"
                disabled={busy}
                onClick={() =>
                  refuse.mutate(c.id, {
                    onSuccess: () => toast.success('Refused'),
                    onError: (error) => toast.error(errorMessage(error)),
                  })
                }
              >
                Refuse
              </Button>
            </div>
          ) : (
            <p role="status" className="text-sm">
              {ended[c.status]}
              {c.result && ` ${c.result}.`} You can go back to your agent.
            </p>
          )}
        </section>
      )}
    </div>
  )
}
