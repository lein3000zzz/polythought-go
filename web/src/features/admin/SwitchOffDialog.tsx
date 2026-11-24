import { useState } from 'react'
import { toast } from 'sonner'

import { useAdminSwitchOff } from '@/api/admin'
import { errorMessage } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

type Props = {
  workflow: { id: string; name: string; active: boolean; ownerEmail?: string }
  owner: string
  open: boolean
  onClose: () => void
  onDone?: () => void
}

/**
 * Switches off a user's workflow for an admin, saying why, which its owner is told, and
 * optionally holds it off until an admin releases it (D435).
 */
export function SwitchOffDialog({ workflow, owner, open, onClose, onDone }: Props) {
  const switchOff = useAdminSwitchOff()
  const [reason, setReason] = useState('')
  const [holding, setHold] = useState(true)
  // One off already is only held: switching it off would do nothing.
  const hold = holding || !workflow.active

  const submit = () =>
    switchOff.mutate(
      { owner, id: workflow.id, reason, hold },
      {
        onSuccess: () => {
          toast.success(hold ? `“${workflow.name}” is off, and held` : `“${workflow.name}” is off`)
          setReason('')
          onClose()
          onDone?.()
        },
        onError: (error) => toast.error(errorMessage(error)),
      },
    )

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {workflow.active ? 'Switch off' : 'Hold off'} “{workflow.name}”?
          </DialogTitle>
          <DialogDescription>
            {workflow.active
              ? `Its triggers stop, and ${workflow.ownerEmail ?? 'its owner'} is told why.`
              : `${workflow.ownerEmail ?? 'Its owner'} is told why.`}{' '}
            Its history says an admin did it.
          </DialogDescription>
        </DialogHeader>

        <form
          id="admin-switch-off"
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor="switch-off-reason">Why</Label>
            <Textarea
              id="switch-off-reason"
              value={reason}
              maxLength={500}
              required
              placeholder="It ran 3,000 times in the last hour."
              onChange={(e) => setReason(e.target.value)}
            />
          </div>
          <div className="flex items-start gap-2 text-sm">
            <Checkbox
              id="switch-off-hold"
              checked={hold}
              disabled={!workflow.active}
              onCheckedChange={(checked) => setHold(checked === true)}
            />
            <Label htmlFor="switch-off-hold" className="flex flex-col items-start gap-0.5">
              Hold it off
              <span className="font-normal text-muted-foreground">
                Its owner cannot switch it on again until an admin releases it.
              </span>
            </Label>
          </div>
        </form>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="admin-switch-off"
            variant="destructive"
            disabled={!reason.trim() || switchOff.isPending}
          >
            {workflow.active ? 'Switch off' : 'Hold off'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
