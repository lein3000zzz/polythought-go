import { Handle, Position, useUpdateNodeInternals, type NodeProps } from '@xyflow/react'
import { cn } from 'cn'
import {
  Ban,
  Box,
  CircleCheck,
  CircleMinus,
  CircleSlash,
  CircleX,
  Hourglass,
  TriangleAlert,
  Zap,
} from 'lucide-react'
import { useEffect, useRef } from 'react'

import { useMe } from '@/api/auth'

import { defaultPort, errorPort, inputsOf, outputsOf, type FlowNode } from './graph'
import { codeOffReason } from './Palette'
import { useNodeProblems } from './problems'
import { useNodeRun, type NodeRun } from './runs'

export function WorkflowNodeView({ id, data, selected }: NodeProps<FlowNode>) {
  const { spec, type } = data
  const inputs = inputsOf(data)
  const outputs = outputsOf(data)
  const problems = useNodeProblems(id)
  const run = useNodeRun(id)
  const Icon = !type ? TriangleAlert : type.trigger ? Zap : Box
  const { data: me } = useMe()
  const codeOff = type?.code === true && me?.mayWriteCode === false

  // React Flow measures handles when a node mounts; ports that come and go later, with
  // "continue on failure" or a setting that gives ports, need a new measurement. Not on
  // mount: an early update makes the initial fit-to-view look at this node alone.
  const updateNodeInternals = useUpdateNodeInternals()
  const ports = `${inputs.join()}|${outputs.join()}`
  const measured = useRef(ports)
  useEffect(() => {
    if (measured.current === ports) return

    measured.current = ports
    updateNodeInternals(id)
  }, [id, ports, updateNodeInternals])

  return (
    <div
      className={cn(
        'relative w-60 rounded-lg border bg-card text-card-foreground shadow-sm',
        selected && 'ring-2 ring-ring',
        (!type || problems.length > 0) && 'border-destructive',
      )}
    >
      {problems.length > 0 && (
        <span
          className="absolute -top-2 -right-2 grid size-5 place-items-center rounded-full bg-destructive text-[10px] font-medium text-white"
          title={problems.map((p) => p.message).join('\n')}
        >
          {problems.length}
        </span>
      )}

      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Icon
          className={cn(
            'size-4 shrink-0',
            !type ? 'text-destructive' : type.trigger ? 'text-amber-500' : 'text-muted-foreground',
          )}
        />
        <span className="truncate text-sm font-medium">
          {spec.name || type?.label || spec.type}
        </span>
        {run && <RunStatus run={run} />}
      </div>

      <div className="px-3 pt-1.5 text-xs text-muted-foreground">
        {type ? spec.type : `${spec.type} (unknown type)`}
      </div>

      {codeOff && (
        <p
          className="flex items-center gap-1.5 px-3 pt-1 text-xs text-amber-600 dark:text-amber-500"
          title={`${codeOffReason} A workflow with this node cannot be saved, and does not run.`}
        >
          <Ban className="size-3 shrink-0" />
          Code is off for you
        </p>
      )}

      {spec.notes && (
        <p className="line-clamp-3 px-3 pt-1 text-xs text-foreground/80 italic" title={spec.notes}>
          {spec.notes}
        </p>
      )}

      <div className="flex justify-between gap-4 py-1.5">
        <Ports ports={inputs} side="in" />
        <Ports ports={outputs} side="out" counts={run?.output} />
      </div>
    </div>
  )
}

// Ports draws one row per port, with its handle on the card's edge. Names are shown only
// when they tell the ports apart: a lone "main" port goes unlabelled.
function Ports({
  ports,
  side,
  counts,
}: {
  ports: string[]
  side: 'in' | 'out'
  counts?: Record<string, number> // items per port in the inspected run
}) {
  const labelled = ports.length > 1 || ports.some((p) => p !== defaultPort)

  return (
    <div className={cn('flex flex-col', side === 'out' && 'items-end')}>
      {ports.map((port) => (
        <div
          key={port}
          className={cn(
            'relative flex h-5 items-center text-[11px] text-muted-foreground',
            side === 'in' ? 'pl-3' : 'pr-3',
            port === errorPort && 'text-destructive',
          )}
        >
          {labelled && (
            <span className="max-w-36 truncate" title={port}>
              {port}
            </span>
          )}
          {counts && (
            <span className="ml-1 font-medium text-foreground tabular-nums">
              {counts[port] ?? 0}
            </span>
          )}
          <Handle
            id={port}
            type={side === 'in' ? 'target' : 'source'}
            position={side === 'in' ? Position.Left : Position.Right}
            className="size-2.5!"
          />
        </div>
      ))}
    </div>
  )
}

const runIcons = {
  succeeded: {
    Icon: CircleCheck,
    className: 'text-emerald-600 dark:text-emerald-400',
    label: 'Succeeded',
  },
  failed: { Icon: CircleX, className: 'text-destructive', label: 'Failed' },
  skipped: {
    Icon: CircleMinus,
    className: 'text-muted-foreground',
    label: 'Skipped: no items reached it',
  },
  waiting: { Icon: Hourglass, className: 'text-amber-500', label: 'Waiting' },
  cancelled: {
    Icon: CircleSlash,
    className: 'text-muted-foreground',
    label: 'Cancelled while it waited',
  },
} as const

function RunStatus({ run }: { run: NodeRun }) {
  const { Icon, className, label } = runIcons[run.status]

  return (
    <span className="ml-auto" title={run.error ?? label}>
      <Icon className={cn('size-4', className)} aria-label={label} />
    </span>
  )
}
