import { ChevronLeft } from 'lucide-react'
import { Link, useParams } from 'react-router'

import { ApiError } from '@/api/client'
import { useCustomNode } from '@/api/customNodes'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Skeleton } from '@/components/ui/skeleton'
import { CustomNodeForm } from '@/features/customnode/CustomNodeForm'

/** Edits the custom node in the URL, or makes one at /nodes/new. */
export function CustomNodePage() {
  const { id } = useParams()
  const node = useCustomNode(id)

  if (node.isError) {
    const notFound = node.error instanceof ApiError && node.error.status === 404

    return (
      <div className="mx-auto flex max-w-xl flex-col gap-4 p-6">
        <Link
          to="/nodes"
          className="flex items-center text-sm text-muted-foreground hover:text-foreground"
        >
          <ChevronLeft className="size-4" />
          Nodes
        </Link>
        {notFound ? (
          <p className="text-sm">This custom node does not exist, or it is not yours.</p>
        ) : (
          <ErrorAlert error={node.error} onRetry={() => void node.refetch()} />
        )}
      </div>
    )
  }

  if (id && !node.data) {
    return (
      <div className="p-6">
        <Skeleton className="h-96" />
      </div>
    )
  }

  // Keyed by the node, so moving to another starts the form over.
  return <CustomNodeForm key={id ?? 'new'} node={node.data} />
}
