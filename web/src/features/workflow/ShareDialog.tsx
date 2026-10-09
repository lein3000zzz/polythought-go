import { useState } from 'react'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'

import { errorMessage, type Workflow } from '@/api/client'
import { useDeleteTemplate, useShareWorkflow, useSharedCopy, type Template } from '@/api/library'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { formatDateTime } from '@/lib/format'

type Props = {
  workflow: Workflow
  dirty: boolean // unsaved changes are not part of what is shared
  open: boolean
  onClose: () => void
}

/** Shares the saved workflow to the library, or updates or removes its shared copy. */
export function ShareDialog({ workflow, dirty, open, onClose }: Props) {
  const existing = useSharedCopy(workflow.id, open)

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent>
        {existing.isPending ? (
          <Skeleton className="h-64" />
        ) : (
          // Mounted per opening, so the fields start from what is shared now.
          <ShareForm
            workflow={workflow}
            dirty={dirty}
            existing={existing.data ?? undefined}
            onClose={onClose}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

type FormProps = {
  workflow: Workflow
  dirty: boolean
  existing?: Template
  onClose: () => void
}

function ShareForm({ workflow, dirty, existing, onClose }: FormProps) {
  const navigate = useNavigate()
  const share = useShareWorkflow(workflow.id)
  const remove = useDeleteTemplate()
  const [summary, setSummary] = useState(existing?.summary ?? '')
  const [description, setDescription] = useState(existing?.description ?? '')

  return (
    <>
      <DialogHeader>
        <DialogTitle>{existing ? 'Update the shared copy' : 'Share to the library'}</DialogTitle>
        <DialogDescription>
          Everyone on this server will see it in the Library and can make their own copy. Wallets
          and webhook URLs are left out: whoever uses it fills in their own.
        </DialogDescription>
      </DialogHeader>

      <form
        id="share-workflow"
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          share.mutate(
            { summary, description },
            {
              onSuccess: (t) => {
                toast.success(existing ? 'Shared copy updated' : 'Shared to the library', {
                  action: { label: 'View', onClick: () => void navigate(`/library/${t.id}`) },
                })
                onClose()
              },
            },
          )
        }}
      >
        {share.isError && <ErrorAlert error={share.error} />}

        {dirty && (
          <p className="rounded-md bg-amber-50 p-2 text-sm">
            This shares the last saved version. Save first to include your latest changes.
          </p>
        )}

        {existing?.updatedAt && (
          <p className="text-sm text-muted-foreground">
            Shared {formatDateTime(existing.updatedAt)}. Sharing again replaces that copy.
          </p>
        )}

        <div className="flex flex-col gap-2">
          <Label htmlFor="share-summary">Summary</Label>
          <Input
            id="share-summary"
            maxLength={200}
            value={summary}
            placeholder="One line on what it does"
            onChange={(e) => setSummary(e.target.value)}
          />
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="share-description">Description</Label>
          <Textarea
            id="share-description"
            rows={6}
            maxLength={4000}
            value={description}
            placeholder={
              'How it works, and what someone should change to make it theirs.\n\nStart lines with "- " for a list.'
            }
            onChange={(e) => setDescription(e.target.value)}
          />
        </div>
      </form>

      <DialogFooter className="sm:justify-between">
        {existing ? (
          <Button
            variant="ghost"
            className="text-destructive"
            disabled={remove.isPending}
            onClick={() =>
              remove.mutate(existing.id, {
                onSuccess: () => {
                  toast.success('Removed from the library')
                  onClose()
                },
                onError: (error) => toast.error(errorMessage(error)),
              })
            }
          >
            Remove from library
          </Button>
        ) : (
          <span />
        )}
        <div className="flex gap-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="share-workflow" disabled={share.isPending}>
            {existing ? 'Update' : 'Share'}
          </Button>
        </div>
      </DialogFooter>
    </>
  )
}
