import { ChevronLeft } from 'lucide-react'
import { useState } from 'react'
import { Link, useParams } from 'react-router'

import { ApiError } from '@/api/client'
import { useNodeTypes, useWorkflow } from '@/api/workflows'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Skeleton } from '@/components/ui/skeleton'
import { WorkflowEditor } from '@/features/workflow/WorkflowEditor'

/** Edits the workflow in the URL, or a new one at /workflows/new. */
export function WorkflowEditorPage() {
  const { id } = useParams()
  const workflow = useWorkflow(id)
  const nodeTypes = useNodeTypes()
  // Bumped to start the editor over from freshly loaded data.
  const [generation, setGeneration] = useState(0)

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

  if ((id !== undefined && workflow.isPending) || nodeTypes.isPending) {
    return (
      <div className="flex flex-col gap-4 p-6">
        {back}
        <Skeleton className="h-96" />
      </div>
    )
  }

  return (
    <WorkflowEditor
      key={`${id ?? 'new'}:${generation}`}
      workflow={workflow.data}
      types={nodeTypes.data}
      onReload={async () => {
        await workflow.refetch()
        setGeneration((g) => g + 1)
      }}
    />
  )
}
