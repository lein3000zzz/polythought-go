import '@xyflow/react/dist/style.css'

import { Background, Controls, ReactFlow, ReactFlowProvider } from '@xyflow/react'
import { useMemo } from 'react'

import type { NodeType } from '@/api/client'
import type { Template } from '@/api/library'
import { useTheme } from '@/lib/theme'

import { toFlow } from './graph'
import { WorkflowNodeView } from './WorkflowNodeView'

// Defined once: React Flow re-mounts every node when this object changes identity.
const nodeTypes = { workflow: WorkflowNodeView }
const fitViewOptions = { padding: 0.15, maxZoom: 1 }

type Props = {
  template: Template
  types: readonly NodeType[]
  onSelect: (nodeId: string | undefined) => void
}

/** A template's graph to look at: nodes can be selected, not moved or connected. */
export function TemplateCanvas({ template, types, onSelect }: Props) {
  const { theme } = useTheme()
  const { nodes, edges } = useMemo(
    () => toFlow({ nodes: template.nodes ?? [], connections: template.connections ?? [] }, types),
    [template, types],
  )

  return (
    <ReactFlowProvider>
      <ReactFlow
        colorMode={theme}
        defaultNodes={nodes}
        defaultEdges={edges}
        nodeTypes={nodeTypes}
        nodesDraggable={false}
        nodesConnectable={false}
        edgesFocusable={false}
        deleteKeyCode={null}
        onNodeClick={(_, node) => onSelect(node.id)}
        onPaneClick={() => onSelect(undefined)}
        fitView
        fitViewOptions={fitViewOptions}
      >
        <Background />
        <Controls fitViewOptions={fitViewOptions} showInteractive={false} />
      </ReactFlow>
    </ReactFlowProvider>
  )
}
