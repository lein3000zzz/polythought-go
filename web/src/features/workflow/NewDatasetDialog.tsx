import { useState } from 'react'

import { errorMessage, type Schemas } from '@/api/client'
import { useCreateDataset } from '@/api/datasets'
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

type Props = {
  limits: Schemas['DatasetLimits']
  onMade: (name: string) => void
  onClose: () => void
}

/**
 * Makes a dataset from a node's settings. One recorded into keeps so many days, and rolls:
 * past so many rows, the oldest go, so recording never stops for want of room.
 */
export function NewDatasetDialog({ limits, onMade, onClose }: Props) {
  const create = useCreateDataset()
  const [name, setName] = useState('')
  const [keyField, setKeyField] = useState('')
  const [keepDays, setKeepDays] = useState(String(limits.keepDays))
  const [keepRows, setKeepRows] = useState(String(limits.rows))

  const submit = () =>
    create.mutate(
      {
        name: name.trim(),
        keyField: keyField.trim() || undefined,
        keepDays: Number(keepDays) || 0,
        keepRows: Number(keepRows) || 0,
      },
      { onSuccess: (d) => onMade(d.name) },
    )

  return (
    <Dialog open onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New dataset</DialogTitle>
          <DialogDescription>
            Yours alone, by its name, which never changes. Notebooks read it with a token.
          </DialogDescription>
        </DialogHeader>

        <form
          id="new-dataset"
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor="new-dataset-name">Name</Label>
            <Input
              id="new-dataset-name"
              value={name}
              required
              maxLength={64}
              pattern="[a-z0-9][a-z0-9_\-]*"
              placeholder="temperature-forecasts"
              aria-describedby="new-dataset-name-hint"
              onChange={(e) => setName(e.target.value)}
            />
            <p id="new-dataset-name-hint" className="text-xs text-muted-foreground">
              Lower-case letters, digits, - and _.
            </p>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="new-dataset-key">Key field</Label>
            <Input
              id="new-dataset-key"
              value={keyField}
              placeholder="city"
              aria-describedby="new-dataset-key-hint"
              onChange={(e) => setKeyField(e.target.value)}
            />
            <p id="new-dataset-key-hint" className="text-xs text-muted-foreground">
              Optional: the field rows are looked up by. Every row then needs it.
            </p>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-2">
              <Label htmlFor="new-dataset-days">Keep days</Label>
              <Input
                id="new-dataset-days"
                type="number"
                min={0}
                value={keepDays}
                onChange={(e) => setKeepDays(e.target.value)}
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="new-dataset-rows">Keep rows</Label>
              <Input
                id="new-dataset-rows"
                type="number"
                min={0}
                max={limits.rows}
                value={keepRows}
                onChange={(e) => setKeepRows(e.target.value)}
              />
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            Rows older than so many days go, and past so many rows the oldest go first. 0 keeps them
            all, up to {limits.rows.toLocaleString('en-US')} rows.
          </p>
          {create.isError && (
            <p role="alert" className="text-sm text-destructive">
              {errorMessage(create.error)}
            </p>
          )}
        </form>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="new-dataset" disabled={!name.trim() || create.isPending}>
            Make it
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
