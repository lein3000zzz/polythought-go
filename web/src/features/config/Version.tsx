import { useServerVersion } from '@/api/settings'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Skeleton } from '@/components/ui/skeleton'
import { appVersion, releaseURL, repoURL } from '@/lib/project'

/**
 * The build of Polythought this server runs (D425), and this page's app beside it where the
 * two differ: a page loaded before an update, or a web image not updated with the server's.
 */
export function Version() {
  const version = useServerVersion()
  const v = version.data
  const release = v && v.version !== 'dev'

  return (
    <section className="flex flex-col gap-3 rounded-lg border p-4">
      <div>
        <h2 className="font-medium">Version</h2>
        <p className="text-sm text-muted-foreground">The Polythought this server runs.</p>
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
    </section>
  )
}
