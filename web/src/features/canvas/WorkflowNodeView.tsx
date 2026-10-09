import { Handle, Position, type NodeProps } from '@xyflow/react'
import { Box, TriangleAlert, Zap } from 'lucide-react'
import { cn } from 'cn'

import { defaultPort, errorPort, type FlowNode } from './flow'

export function WorkflowNodeView({ data, selected }: NodeProps<FlowNode>) {
  const { spec, type, inputs, outputs } = data
  const Icon = !type ? TriangleAlert : type.trigger ? Zap : Box

  return (
    <div
      className={cn(
        'w-60 rounded-lg border bg-card text-card-foreground shadow-sm',
        selected && 'ring-2 ring-ring',
        !type && 'border-destructive',
      )}
    >
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
            isConnectable={false}
          />
        </div>
      ))}
    </div>
  )
}
