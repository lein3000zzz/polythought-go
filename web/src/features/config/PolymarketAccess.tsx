import { RefreshCw } from 'lucide-react'
import { toast } from 'sonner'

import { errorMessage } from '@/api/client'
import { useCheckGeoblock, useGeoblock } from '@/api/settings'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { formatDateTime } from '@/lib/format'

const geoblockTiers = 'https://docs.polymarket.com/api-reference/geoblock'

/**
 * Whether Polymarket takes orders from this server's address, as it last answered: checked
 * at start, every 6 hours and on Check now (D186). A warning only (D187).
 */
export function PolymarketAccess() {
  const geo = useGeoblock()
  const check = useCheckGeoblock()
  const g = geo.data
  const where = g && [g.country, g.region].filter(Boolean).join(', ')

  return (
    <section className="flex flex-col gap-3 rounded-lg border p-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="font-medium">Polymarket access</h2>
          <p className="text-sm text-muted-foreground">
            Whether Polymarket takes orders from this server&apos;s address, checked at start and
            every 6 hours.
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          disabled={check.isPending}
          onClick={() =>
            check.mutate(undefined, { onError: (error) => toast.error(errorMessage(error)) })
          }
        >
          <RefreshCw />
          {check.isPending ? 'Checking…' : 'Check now'}
        </Button>
      </div>

      {geo.isPending && <Skeleton className="h-10" />}
      {geo.isError && <ErrorAlert error={geo.error} onRetry={() => void geo.refetch()} />}

      {g && !g.checked && !g.error && (
        <p className="text-sm text-muted-foreground">Not checked yet.</p>
      )}

      {g?.checked &&
        (g.blocked ? (
          <Alert variant="destructive">
            <AlertTitle>Polymarket blocks orders from this server</AlertTitle>
            <AlertDescription>
              <p>
                It answers blocked for {where}, from {g.ip}. In a restricted region it refuses new
                orders, but closing positions goes on; in a few it restricts only its website, and
                its API takes orders, which the answer does not tell apart. See{' '}
                <a href={geoblockTiers} target="_blank" rel="noreferrer" className="underline">
                  its list of regions
                </a>
                . Paper trading is not affected.
              </p>
            </AlertDescription>
          </Alert>
        ) : (
          <p className="text-sm">
            Polymarket takes orders from this server: {where}, from {g.ip}.
          </p>
        ))}

      {g?.checkedAt && (
        <p className="text-xs text-muted-foreground">Answered {formatDateTime(g.checkedAt)}.</p>
      )}
      {g?.error && (
        <p className="text-sm text-destructive">
          The last check failed: {g.error}
          {g.checked && '. The answer above is from before.'}
        </p>
      )}
    </section>
  )
}
