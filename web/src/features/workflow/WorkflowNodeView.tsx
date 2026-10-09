import { Handle, Position, useUpdateNodeInternals, type NodeProps } from '@xyflow/react'
import { cn } from 'cn'
import { Box, TriangleAlert, Zap } from 'lucide-react'
import { useEffect, useRef } from 'react'

import { defaultPort, errorPort, inputsOf, outputsOf, type FlowNode } from './graph'
import { useNodeProblems } from './problems'

export function WorkflowNodeView({ id, data, selected }: NodeProps<FlowNode>) {
  const { spec, type } = data
  const inputs = inputsOf(data)
  const outputs = outputsOf(data)
  const problems = useNodeProblems(id)
  const Icon = !type ? TriangleAlert : type.trigger ? Zap : Box

  // React Flow measures handles when a node mounts; ports that come and go later, with
  // "continue on failure", need a new measurement. Not on mount: an early update makes
  // the initial fit-to-view look at this node alone.
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
      </div>

      <div className="px-3 pt-1.5 text-xs text-muted-foreground">
        {type ? spec.type : `${spec.type} (unknown type)`}
      </div>

      <div className="flex justify-between gap-4 py-1.5">
        <Ports ports={inputs} side="in" />
        <Ports ports={outputs} side="out" />
      </div>
    </div>
  )
}

// Ports draws one row per port, with its handle on the card's edge. Names are shown only
// when they tell the ports apart: a lone "main" port goes unlabelled.
function Ports({ ports, side }: { ports: string[]; side: 'in' | 'out' }) {
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
          {labelled && port}
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
