import { BookOpen, Users } from 'lucide-react'
import { Link } from 'react-router'

import { useTemplates, type Template } from '@/api/library'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

export function LibraryPage() {
  const templates = useTemplates()
  const all = templates.data?.pages.flatMap((page) => page.templates) ?? []
  const builtIn = all.filter((t) => t.builtIn)
  const shared = all.filter((t) => !t.builtIn)

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-10 p-6">
      <div className="flex flex-col gap-1">
        <h1 className="font-heading text-xl font-semibold">Library</h1>
        <p className="text-sm text-muted-foreground">
          Ready-made workflows. Open one to see how it works, then make your own copy and change it
          as you like.
        </p>
      </div>

      {templates.isPending && <Skeleton className="h-48" />}
      {templates.isError && (
        <ErrorAlert error={templates.error} onRetry={() => void templates.refetch()} />
      )}

      {templates.isSuccess && (
        <>
          <section className="flex flex-col gap-4">
            <div>
              <h2 className="flex items-center gap-2 font-medium">
                <BookOpen className="size-4" />
                Scenarios
              </h2>
              <p className="text-sm text-muted-foreground">
                Made to show what workflows can do. New here? Start with the first one.
              </p>
            </div>
            <Grid templates={builtIn} />
          </section>

          <section className="flex flex-col gap-4">
            <div>
              <h2 className="flex items-center gap-2 font-medium">
                <Users className="size-4" />
                Shared by members
              </h2>
              <p className="text-sm text-muted-foreground">
                Share one of yours from its editor, under the ⋮ menu. Wallets and webhook URLs stay
                private.
              </p>
            </div>

            {shared.length > 0 ? (
              <Grid templates={shared} />
            ) : (
              <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
                Nothing shared yet.
              </p>
            )}

            {templates.hasNextPage && (
              <Button
                variant="outline"
                className="self-center"
                onClick={() => void templates.fetchNextPage()}
                disabled={templates.isFetchingNextPage}
              >
                Load more
              </Button>
            )}
          </section>
        </>
      )}
    </div>
  )
}

function Grid({ templates }: { templates: Template[] }) {
  return (
    <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {templates.map((t) => (
        <li key={t.id}>
          <TemplateCard template={t} />
        </li>
      ))}
    </ul>
  )
}

function TemplateCard({ template: t }: { template: Template }) {
  const asks = inputSummary(t)

  return (
    <Link
      to={`/library/${t.id}`}
      className="block h-full rounded-xl focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
    >
      <Card className="h-full transition-colors hover:bg-muted/40">
        <CardHeader>
          <CardTitle>{t.name}</CardTitle>
          {t.summary && <CardDescription>{t.summary}</CardDescription>}
        </CardHeader>
        <CardFooter className="mt-auto flex flex-wrap gap-2">
          {t.mine && <Badge>Yours</Badge>}
          {asks && <Badge variant="outline">Asks for {asks}</Badge>}
          <Badge variant="secondary">
            {t.nodeTypes.some((n) => n.startsWith('trigger.') && n !== 'trigger.manual')
              ? 'Runs on its own'
              : 'Run by hand'}
          </Badge>
        </CardFooter>
      </Card>
    </Link>
  )
}

// inputSummary names what using a template needs: "a wallet", "a webhook URL". Optional
// inputs, like a credential, are left out.
function inputSummary(t: Template): string | undefined {
  const asks = [
    ...new Set(
      t.inputs
        .filter((i) => i.param.required)
        .map((i) => {
          const what = lowerFirst(i.param.label ?? i.param.name)

          if (i.param.kind === 'wallet') return 'a wallet'
          if (i.param.kind === 'table') return what // rows of them: "traders"

          return `a ${what}`
        }),
    ),
  ]

  return asks.length > 0 ? asks.join(' and ') : undefined
}

function lowerFirst(s: string): string {
  return s.charAt(0).toLowerCase() + s.slice(1)
}
