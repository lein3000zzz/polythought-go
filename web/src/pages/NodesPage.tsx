import { Plus } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { ApiError, errorMessage } from '@/api/client'
import {
  useCustomNodes,
  useDeleteCustomNode,
  type BrokenWorkflow,
  type CustomNode,
} from '@/api/customNodes'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { BreaksDialog } from '@/features/customnode/BreaksDialog'
import { formatDateTime } from '@/lib/format'

/** The user's custom nodes (D57). */
export function NodesPage() {
  const list = useCustomNodes()
  const { data: me } = useMe()
  const codeOff = me?.mayWriteCode === false

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-6 p-6">
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h1 className="font-heading text-xl font-semibold">Custom nodes</h1>
          <p className="text-sm text-muted-foreground">
            Node types of your own: settings, outputs and items you choose, and TypeScript that does
            the work. Only your workflows see them, in the Custom group of the node list.{' '}
            <a href="/docs/guides/custom-nodes/" target="_blank" className="underline">
              Write your first custom node
            </a>
          </p>
        </div>
        {codeOff ? (
          <Button disabled>
            <Plus />
            New custom node
          </Button>
        ) : (
          <Button asChild>
            <Link to="/nodes/new">
              <Plus />
              New custom node
            </Link>
          </Button>
        )}
      </div>

      {codeOff && (
        <Alert>
          <AlertDescription>
            An admin has turned code off for you: you can look at your custom nodes and delete them,
            not make or change them, and workflows that use them do not run.
          </AlertDescription>
        </Alert>
      )}

      {list.isPending && <Skeleton className="h-32" />}
      {list.isError && <ErrorAlert error={list.error} onRetry={() => void list.refetch()} />}
      {list.data?.length === 0 && (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
          No custom nodes yet.
        </p>
      )}

      {list.data && list.data.length > 0 && <NodeTable nodes={list.data} />}
    </div>
  )
}

function NodeTable({ nodes }: { nodes: CustomNode[] }) {
  const remove = useDeleteCustomNode()
  const [deleting, setDeleting] = useState<CustomNode>()
  const [breaks, setBreaks] = useState<{ node: CustomNode; workflows: BrokenWorkflow[] }>()

  const deleteNode = (node: CustomNode, confirm = false) =>
    remove.mutate(
      { id: node.id, confirm },
      {
        onSuccess: () => {
          setBreaks(undefined)
          toast.success(`Deleted ${node.type}`)
        },
        onError: (error) => {
          if (error instanceof ApiError && error.code === 'breaks_workflows') {
            setBreaks({ node, workflows: error.workflows })
          } else {
            toast.error(errorMessage(error))
          }
        },
      },
    )

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Node</TableHead>
            <TableHead>Type</TableHead>
            <TableHead>Outputs</TableHead>
            <TableHead>Changed</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {nodes.map((n) => (
            <TableRow key={n.id}>
              <TableCell className="font-medium">
                <Link to={`/nodes/${n.id}`} className="hover:underline">
                  {n.label}
                </Link>
              </TableCell>
              <TableCell className="font-mono text-xs">{n.type}</TableCell>
              <TableCell>{n.outputs.join(', ')}</TableCell>
              <TableCell>{formatDateTime(n.updatedAt)}</TableCell>
              <TableCell className="text-right">
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-destructive"
                  onClick={() => setDeleting(n)}
                >
                  Delete
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <ConfirmDialog
        open={deleting !== undefined}
        title={`Delete ${deleting?.label ?? ''}?`}
        description="It goes for good. Workflows that use it are listed before anything goes."
        confirm="Delete"
        destructive
        onConfirm={() => {
          if (deleting) deleteNode(deleting)
          setDeleting(undefined)
        }}
        onCancel={() => setDeleting(undefined)}
      />

      {breaks && (
        <BreaksDialog
          workflows={breaks.workflows}
          confirm="Delete anyway"
          onCancel={() => setBreaks(undefined)}
          onConfirm={() => deleteNode(breaks.node, true)}
        />
      )}
    </>
  )
}
