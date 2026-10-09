import '@xyflow/react/dist/style.css'

import { Background, Controls, ReactFlow } from '@xyflow/react'
import { useMemo } from 'react'

import type { NodeType, Workflow } from '@/api/client'

import { toFlow } from './flow'
import { WorkflowNodeView } from './WorkflowNodeView'

// Defined once: React Flow re-mounts every node when this object changes identity.
const nodeTypes = { workflow: WorkflowNodeView }

type Props = {
  workflow: Workflow
  types: readonly NodeType[]
  onSelect: (nodeId: string | undefined) => void
}

/** A read-only view of a workflow's graph. Clicking a node selects it. */
export function WorkflowCanvas({ workflow, types, onSelect }: Props) {
  const { nodes, edges } = useMemo(() => toFlow(workflow, types), [workflow, types])

  return (
    <ReactFlow
      // Uncontrolled: React Flow keeps selection and measurements itself. A new version of
      // the workflow remounts the canvas.
      key={`${workflow.id}@${workflow.version}`}
      defaultNodes={nodes}
      defaultEdges={edges}
      nodeTypes={nodeTypes}
      nodesDraggable={false}
      nodesConnectable={false}
      edgesFocusable={false}
      onNodeClick={(_, node) => onSelect(node.id)}
      onPaneClick={() => onSelect(undefined)}
      fitView
      fitViewOptions={{ padding: 0.2, maxZoom: 1 }}
    >
      <Background />
      <Controls showInteractive={false} />
    </ReactFlow>
  )
}
