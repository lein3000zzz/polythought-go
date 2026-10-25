import { Copy, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { errorMessage, type Schemas } from '@/api/client'
import {
  UpdateFailedError,
  UpdateTimeoutError,
  useApplyUpdate,
  useCheckUpdates,
  useServerVersion,
  useUpdates,
} from '@/api/settings'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { formatDateTime } from '@/lib/format'
import { appVersion, releaseURL, repoURL } from '@/lib/project'

import { configPath } from './pages'

/** How to update by hand, from the folder with the Compose file (D425). */
const byHand = [
  'docker compose -f deployments/docker-compose.yml --env-file .env --profile app pull',
  'docker compose -f deployments/docker-compose.yml --env-file .env --profile app up -d',
].join('\n')

const updatesDocs = '/docs/self-hosting/install/#updates'

/**
 * The build of Polythought this server runs (D425), and this page's app beside it where the
 * two differ: a page loaded before an update, or a web image not updated with the server's.
 * Below, whether a newer release is out (D424), and updating to it.
 */
export function Version() {
  const version = useServerVersion()
  const updates = useUpdates()
  const check = useCheckUpdates()
  // Here, as the server it updated says no newer one is out once it is back.
  const apply = useApplyUpdate()
  const v = version.data
  const release = v && v.version !== 'dev'

  return (
    <section className="flex flex-col gap-3 rounded-lg border p-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="font-medium">Version</h2>
          <p className="text-sm text-muted-foreground">
            The Polythought this server runs, and whether a newer one is out.
          </p>
        </div>
        {updates.data?.on && (
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
        )}
      </div>

      {version.isPending && <Skeleton className="h-6" />}
      {version.isError && (
        <ErrorAlert error={version.error} onRetry={() => void version.refetch()} />
      )}

      {v && (
        <p className="text-sm">
          {release ? (
            <a
              href={releaseURL(v.version)}
              target="_blank"
              rel="noreferrer"
              className="font-medium underline underline-offset-4"
            >
              {v.version}
            </a>
          ) : (
            <span className="font-medium">A development build</span>
          )}
          {v.commit && (
            <>
              {', commit '}
              <a
                href={`${repoURL}/commit/${v.commit}`}
                target="_blank"
                rel="noreferrer"
                className="font-mono underline underline-offset-4"
              >
                {v.commit.slice(0, 7)}
              </a>
            </>
          )}
          {v.modified && ', with changes not committed'}
          <span className="text-muted-foreground">, built with {v.go}.</span>
        </p>
      )}

      {v && v.version !== appVersion && (
        <p className="text-sm text-muted-foreground">
          This page&apos;s app is {appVersion === 'dev' ? 'a development build' : appVersion}:
          reload it to get the server&apos;s, or update the web image too.
        </p>
      )}

      {updates.isError && (
        <ErrorAlert error={updates.error} onRetry={() => void updates.refetch()} />
      )}
      {apply.isSuccess ? (
        <div className="flex flex-wrap items-center justify-between gap-3 text-sm">
          <p>Updated to {apply.variables}. Reload the page to use its app.</p>
          <Button size="sm" onClick={() => window.location.reload()}>
            Reload
          </Button>
        </div>
      ) : (
        updates.data && <Updates u={updates.data} apply={apply} />
      )}
    </section>
  )
}

type Apply = ReturnType<typeof useApplyUpdate>

function Updates({ u, apply }: { u: Schemas['Updates']; apply: Apply }) {
  if (!u.on) {
    return (
      <p className="text-sm text-muted-foreground">
        The update check is off: turn on{' '}
        <Link to={configPath('updates', 'updates.check')} className="underline underline-offset-4">
          Check for updates
        </Link>{' '}
        to hear of new releases.
      </p>
    )
  }

  const latest = u.latest

  return (
    <>
      {latest && u.newer && <Newer u={u} latest={latest} apply={apply} />}
      {latest && !u.newer && (
        <p className="text-sm">
          {u.current === 'dev' ? 'The newest release is ' : 'This is the newest release: '}
          <a
            href={latest.url}
            target="_blank"
            rel="noreferrer"
            className="font-medium underline underline-offset-4"
          >
            {latest.version}
          </a>
          .
        </p>
      )}
      {!latest && !u.error && <p className="text-sm text-muted-foreground">Not checked yet.</p>}

      {u.checkedAt && (
        <p className="text-xs text-muted-foreground">Checked {formatDateTime(u.checkedAt)}.</p>
      )}
      {u.error && (
        <p className={latest ? 'text-sm text-destructive' : 'text-sm text-muted-foreground'}>
          {latest
            ? `The last check failed: ${u.error}. The answer above is from before.`
            : `${u.error}.`}
        </p>
      )}
    </>
  )
}

function Newer({
  u,
  latest,
  apply,
}: {
  u: Schemas['Updates']
  latest: Schemas['Release']
  apply: Apply
}) {
  const [asking, setAsking] = useState(false)
  const failed = apply.error instanceof UpdateFailedError ? apply.error : undefined

  return (
    <Alert>
      <AlertTitle>Polythought {latest.version} is out</AlertTitle>
      <AlertDescription className="flex flex-col gap-3">
        <p>
          Released {formatDateTime(latest.publishedAt)}
          {latest.prerelease && ', a pre-release'}.{' '}
          <a href={latest.url} target="_blank" rel="noreferrer" className="underline">
            See what changed
          </a>
          .
        </p>

        {u.updater ? (
          <>
            {u.updaterError && (
              <p className="text-destructive">The updater does not answer: {u.updaterError}</p>
            )}
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p>
                {apply.isPending
                  ? `Updating to ${latest.version}: pulling its images, then restarting the server. This page waits for it.`
                  : 'The updater pulls its images and restarts the server with them.'}
              </p>
              <Button size="sm" disabled={apply.isPending} onClick={() => setAsking(true)}>
                {apply.isPending ? 'Updating…' : `Update to ${latest.version}`}
              </Button>
            </div>
            {apply.isError && <p className="text-destructive">{updateError(apply.error)}</p>}
            {failed?.log && (
              <pre className="max-h-64 overflow-auto rounded-md bg-muted p-2 font-mono text-xs whitespace-pre-wrap">
                {failed.log}
              </pre>
            )}
          </>
        ) : (
          <>
            <p>
              Update from the folder with the Compose file, with POLYTHOUGHT_VERSION in .env at{' '}
              {latest.version} or unset. Or{' '}
              <a href={updatesDocs} target="_blank" className="underline">
                set up the updater
              </a>{' '}
              to update from here.
            </p>
            <CodeBlock code={byHand} />
          </>
        )}
      </AlertDescription>

      <ConfirmDialog
        open={asking}
        title={`Update to ${latest.version}?`}
        description="The updater pulls the new images, then restarts the server with them, which updates the database as it starts: back it up first. Active workflows stop and start again, runs in progress are cut short, and open pages reconnect."
        confirm="Update"
        onCancel={() => setAsking(false)}
        onConfirm={() => {
          setAsking(false)
          apply.mutate(latest.version)
        }}
      />
    </Alert>
  )
}

/** What went wrong with an update: the updater's account, the wait, or the server's answer. */
function updateError(error: Error): string {
  if (error instanceof UpdateFailedError) return `The update failed: ${error.message}.`
  if (error instanceof UpdateTimeoutError) return error.message

  return errorMessage(error)
}

function CodeBlock({ code }: { code: string }) {
  return (
    <div className="flex items-start gap-2">
      <pre className="min-w-0 flex-1 overflow-x-auto rounded-md bg-muted p-2 font-mono text-xs">
        {code}
      </pre>
      <Button
        variant="outline"
        size="icon"
        aria-label="Copy"
        onClick={() => void navigator.clipboard.writeText(code).then(() => toast.success('Copied'))}
      >
        <Copy />
      </Button>
    </div>
  )
}
