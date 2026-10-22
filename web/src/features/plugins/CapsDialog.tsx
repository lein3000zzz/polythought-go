import { useState } from 'react'
import { toast } from 'sonner'

import { ApiError } from '@/api/client'
import { type Plugin, type PluginCaps, useSetPluginCaps } from '@/api/plugins'
import { useSettings } from '@/api/settings'
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

type Props = {
  plugin: Plugin
  onClose: () => void
}

/** The caps a card may set, by the API's field, with the Config page's setting each overrides. */
const fields = [
  {
    key: 'dataCap',
    setting: 'plugins.data_cap',
    label: 'Data (MiB)',
    about: 'Its directory and its temporary one together. Past it, it is stopped as crashed.',
  },
  {
    key: 'memoryCap',
    setting: 'plugins.memory_cap',
    label: 'Memory (MiB)',
    about: 'Its process. Past it, the process is killed and started again, as after a crash.',
  },
] as const

/**
 * A plugin's own caps (D346, D350): each blank for the Config page's, which it shows. They
 * apply as it is next measured; a Go plugin's soft memory limit from its next start.
 */
export function CapsDialog({ plugin, onClose }: Props) {
  const settings = useSettings()
  const save = useSetPluginCaps(plugin.id)
  const [drafts, setDrafts] = useState<Record<keyof PluginCaps, string>>({
    dataCap: plugin.caps.dataCap ? String(plugin.caps.dataCap) : '',
    memoryCap: plugin.caps.memoryCap ? String(plugin.caps.memoryCap) : '',
  })

  const configured = (key: string) => settings.data?.settings.find((s) => s.key === key)?.value
  const problem = (param: string) =>
    save.error instanceof ApiError
      ? save.error.problems.find((p) => p.param === param)?.message
      : undefined

  const submit = () => {
    const caps: PluginCaps = {
      dataCap: drafts.dataCap.trim() === '' ? 0 : Number(drafts.dataCap),
      memoryCap: drafts.memoryCap.trim() === '' ? 0 : Number(drafts.memoryCap),
    }

    save.mutate(caps, {
      onSuccess: () => {
        toast.success(`${plugin.name}'s caps saved: they apply as it is next measured`)
        onClose()
      },
    })
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{plugin.name}: caps</DialogTitle>
          <DialogDescription>
            What it may take. Leave one blank for the Config page&apos;s.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          {fields.map((f) => {
            const id = `plugin-cap-${f.key}`
            const config = configured(f.setting)
            const why = problem(f.key)

            return (
              <div key={f.key} className="flex flex-col gap-1.5">
                <Label htmlFor={id}>{f.label}</Label>
                <Input
                  id={id}
                  type="number"
                  min={1}
                  inputMode="numeric"
                  placeholder={
                    config === undefined
                      ? "The Config page's"
                      : `The Config page's: ${String(config)}`
                  }
                  value={drafts[f.key]}
                  onChange={(e) => setDrafts((was) => ({ ...was, [f.key]: e.target.value }))}
                />
                <p className="text-xs text-muted-foreground">{f.about}</p>
                {why && <p className="text-xs text-destructive">{why}</p>}
              </div>
            )
          })}
        </div>

        {save.error && !(save.error instanceof ApiError && save.error.problems.length > 0) && (
          <ErrorAlert error={save.error} />
        )}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={save.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
