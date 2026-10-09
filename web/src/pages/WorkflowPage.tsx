import { ChevronLeft } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router'

import { ApiError } from '@/api/client'
import { useNodeTypes, useWorkflow } from '@/api/workflows'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { NodeDetails } from '@/features/canvas/NodeDetails'
import { WorkflowCanvas } from '@/features/canvas/WorkflowCanvas'

export function WorkflowPage() {
  const { id = '' } = useParams()
  const workflow = useWorkflow(id)
  const nodeTypes = useNodeTypes()
  const [selected, setSelected] = useState<string>()

  const back = (
    <Link
      to="/workflows"
      className="flex items-center text-sm text-muted-foreground hover:text-foreground"
    >
      <ChevronLeft className="size-4" />
      Workflows
    </Link>
  )

  if (workflow.isError || nodeTypes.isError) {
    const error = workflow.error ?? nodeTypes.error
    const notFound = error instanceof ApiError && error.status === 404

    return (
      <div className="mx-auto flex max-w-xl flex-col gap-4 p-6">
        {back}
        {notFound ? (
          <p className="text-sm">This workflow does not exist, or it is not yours.</p>
        ) : (
          <ErrorAlert
            error={error}
            onRetry={() => {
              void workflow.refetch()
              void nodeTypes.refetch()
            }}
          />
        )}
      </div>
    )
  }

  if (workflow.isPending || nodeTypes.isPending) {
    return (
      <div className="flex flex-col gap-4 p-6">
        {back}
        <Skeleton className="h-96" />
      </div>
    )
  }

  const w = workflow.data
  const spec = w.nodes.find((n) => n.id === selected)

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-3 border-b px-4 py-3">
        {back}
        <span className="text-muted-foreground">/</span>
        <h1 className="font-heading font-semibold">{w.name}</h1>
        <Badge variant={w.active ? 'default' : 'secondary'}>
          {w.active ? 'Active' : 'Inactive'}
        </Badge>
        <span className="text-xs text-muted-foreground">Version {w.version}</span>
        <span className="ml-auto text-xs text-muted-foreground">Read-only</span>
      </div>

      <div className="flex min-h-0 flex-1">
        <div className="min-w-0 flex-1">
          <WorkflowCanvas workflow={w} types={nodeTypes.data} onSelect={setSelected} />
        </div>
        {spec && (
          <NodeDetails
            spec={spec}
            type={nodeTypes.data.find((t) => t.name === spec.type)}
            onClose={() => setSelected(undefined)}
          />
        )}
      </div>
    </div>
  )
}
