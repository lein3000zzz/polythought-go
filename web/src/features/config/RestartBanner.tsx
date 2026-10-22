import { RefreshCw } from 'lucide-react'
import { Fragment, useState } from 'react'
import { Link } from 'react-router'

import type { Setting } from '@/api/settings'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { configPath, slugOf } from './pages'

type BannerProps = {
  settings: Setting[]
  restarting: boolean
  onRestart: () => void
}

export function RestartBanner({ settings, restarting, onRestart }: BannerProps) {
  const [asking, setAsking] = useState(false)
  const pending = settings.filter((s) => s.pending)

  return (
    <Alert>
      <RefreshCw />
      <AlertTitle>A restart is needed</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-3">
        <p>
          Changes to{' '}
          {pending.map((s, i) => (
            <Fragment key={s.key}>
              {i > 0 && ', '}
              {/* Each on its own page, as the banner is on all of them (D391). */}
              <Link
                to={configPath(slugOf(s.group), s.key)}
                className="underline underline-offset-4"
              >
                {s.label}
              </Link>
            </Fragment>
          ))}{' '}
          take effect when the server restarts.
        </p>
        <Button size="sm" onClick={() => setAsking(true)} disabled={restarting}>
          {restarting ? 'Restarting…' : 'Restart now'}
        </Button>
      </AlertDescription>

      <ConfirmDialog
        open={asking}
        title="Restart the server?"
        description="It stops and starts again, which takes a few seconds. Active workflows stop and start again, runs in progress are cut short, and open pages reconnect."
        confirm="Restart"
        onCancel={() => setAsking(false)}
        onConfirm={() => {
          setAsking(false)
          onRestart()
        }}
      />
    </Alert>
  )
}
