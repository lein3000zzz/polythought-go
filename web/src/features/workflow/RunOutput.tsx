import { CircleAlert } from 'lucide-react'

import type { Schemas } from '@/api/client'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { formatDuration, formatTime } from '@/lib/format'

import { errorPort } from './graph'
import type { NodeRun } from './runs'

type Props = {
  run?: NodeRun // undefined: the node did not exist when the execution ran
  kept: boolean // whether the execution kept item samples at all
}

/** What one node did in the inspected execution: its status, counts and sample items. */
export function RunOutput({ run, kept }: Props) {
  if (!run) {
    return (
      <p className="p-4 text-sm text-muted-foreground">
        This node was not part of the workflow when this execution ran.
      </p>
    )
  }

  const ports = Object.entries(run.output ?? {}).sort(([a], [b]) => portOrder(a) - portOrder(b))

  return (
    <div className="flex flex-col gap-4 p-4 text-sm">
      <p className="text-muted-foreground">{runSummary(run)}</p>

      {run.error && (
        <Alert variant="destructive">
          <CircleAlert />
          <AlertDescription className="break-words">{run.error}</AlertDescription>
        </Alert>
      )}

      {ports.length > 0 && !kept && (
        <p className="text-muted-foreground">
          Items are kept for runs started with Run only; this one has counts alone.
        </p>
      )}

      {ports.map(([port, count]) => (
        <Port
          key={port}
          port={port}
          count={count}
          samples={run.samples?.[port] ?? []}
          kept={kept}
        />
      ))}
    </div>
  )
}

type PortProps = {
  port: string
  count: number
  samples: Schemas['ItemSample'][]
  kept: boolean
}

function Port({ port, count, samples, kept }: PortProps) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className={port === errorPort ? 'font-medium text-destructive' : 'font-medium'}>
        {port} · {count} {count === 1 ? 'item' : 'items'}
        {kept && samples.length > 0 && samples.length < count && (
          <span className="font-normal text-muted-foreground"> (first {samples.length} shown)</span>
        )}
      </h3>

      {kept && samples.length === 0 && count > 0 && (
        <p className="text-xs text-muted-foreground">Too large to keep.</p>
      )}

      {samples.map((s, i) => (
        <div key={i} className="flex flex-col gap-1">
          {s.error && <p className="text-xs text-destructive">{s.error}</p>}
          {s.json && (
            <pre className="max-h-64 overflow-auto rounded-md bg-muted p-2 font-mono text-xs">
              {JSON.stringify(s.json, null, 2)}
            </pre>
          )}
        </div>
      ))}
    </section>
  )
}

// portOrder lists the main port first and the error port last.
function portOrder(port: string): number {
  return port === 'main' ? 0 : port === errorPort ? 2 : 1
}

function runSummary(run: NodeRun): string {
  switch (run.status) {
    case 'skipped':
      return 'Skipped: no items reached this node.'
    case 'waiting':
      return `Waiting since ${formatTime(run.startedAt)}.`
    case 'cancelled':
      return 'Cancelled while it waited.'
  }

  const took = run.finishedAt ? Date.parse(run.finishedAt) - Date.parse(run.startedAt) : 0

  return `${run.status === 'failed' ? 'Failed' : 'Succeeded'} in ${formatDuration(took)}.`
}
