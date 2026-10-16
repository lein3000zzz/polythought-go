import { ExternalLink } from 'lucide-react'
import { Link } from 'react-router'

import type { BrokenWorkflow } from '@/api/customNodes'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

type Props = {
  workflows: BrokenWorkflow[]
  /** What going ahead does: "Save anyway", "Delete anyway". */
  confirm: string
  onConfirm: () => void
  onCancel: () => void
}

/**
 * Says which workflows a change to a custom node breaks, and how, before making it (D50).
 * Going ahead, they fail when they run until they are edited.
 */
export function BreaksDialog({ workflows, confirm, onConfirm, onCancel }: Props) {
  return (
    <AlertDialog open onOpenChange={(open) => !open && onCancel()}>
      <AlertDialogContent className="sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>
            This breaks {workflows.length === 1 ? 'a workflow' : `${workflows.length} workflows`}
          </AlertDialogTitle>
          <AlertDialogDescription>
            Workflows always use the latest version of the node. Going ahead, these fail when they
            run until you edit them; opening one marks what no longer holds.
          </AlertDialogDescription>
        </AlertDialogHeader>

        <ul className="flex max-h-72 flex-col gap-3 overflow-y-auto text-sm">
          {workflows.map((w) => (
            <li key={w.id} className="flex flex-col gap-1">
              <Link
                to={`/workflows/${w.id}`}
                target="_blank"
                rel="noreferrer"
                className="flex items-center gap-1 font-medium hover:underline"
              >
                {w.name}
                <ExternalLink className="size-3.5 text-muted-foreground" />
              </Link>
              <ul className="list-disc pl-5 text-muted-foreground">
                {w.problems.map((p, i) => (
                  <li key={i}>{p.message}</li>
                ))}
              </ul>
            </li>
          ))}
        </ul>

        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={onConfirm}>
            {confirm}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
