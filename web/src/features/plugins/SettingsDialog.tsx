import { useState } from 'react'
import { toast } from 'sonner'

import {
  type Plugin,
  type PluginSetting,
  usePluginSettings,
  useUpdatePluginSettings,
} from '@/api/plugins'
import { ApiError } from '@/api/client'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

type Props = {
  plugin: Plugin
  onClose: () => void
}

/** A field's value as the form holds it: text, or on and off. */
type Draft = string | boolean

/** draftOf is a setting's value as its field shows it; a secret's is empty. */
function draftOf(s: PluginSetting): Draft {
  if (s.secret) return ''
  if (s.kind === 'bool') return s.value === true
  if (s.kind === 'list') return Array.isArray(s.value) ? (s.value as string[]).join('\n') : ''

  return s.value === null || s.value === undefined ? '' : String(s.value)
}

/** valueOf is what the API takes for a field's draft. */
function valueOf(s: PluginSetting, d: Draft): unknown {
  if (s.kind === 'bool') return d === true
  if (s.kind === 'int') return d === '' ? null : Number(d)
  if (s.kind === 'list') {
    return String(d)
      .split('\n')
      .map((l) => l.trim())
      .filter(Boolean)
  }

  return d
}

/**
 * A plugin's own settings (D329), as the version it runs declares them: its runs get
 * them from the next one. Secrets are write-only, as on the Config page.
 */
export function SettingsDialog({ plugin, onClose }: Props) {
  const settings = usePluginSettings(plugin.id)
  const save = useUpdatePluginSettings(plugin.id)
  const [drafts, setDrafts] = useState<Record<string, Draft>>({})

  const list = settings.data?.settings ?? []
  const draft = (s: PluginSetting) => drafts[s.key] ?? draftOf(s)
  const set = (key: string, d: Draft) => setDrafts((was) => ({ ...was, [key]: d }))
  const problem = (key: string) =>
    save.error instanceof ApiError
      ? save.error.problems.find((p) => p.param === key)?.message
      : undefined

  const submit = () => {
    const values: Record<string, unknown> = {}

    for (const s of list) {
      const d = drafts[s.key]
      if (d === undefined) continue
      if (s.secret && d === '') continue // unchanged
      if (!s.secret && d === draftOf(s)) continue

      values[s.key] = valueOf(s, d)
    }

    if (Object.keys(values).length === 0) {
      onClose()
      return
    }

    save.mutate(values, {
      onSuccess: () => {
        toast.success(`${plugin.name}'s settings saved: its runs get them from the next`)
        onClose()
      },
    })
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{plugin.name}: settings</DialogTitle>
          <DialogDescription>
            Its own, as its version {plugin.version} declares them. Its runs get them from the next
            one.
          </DialogDescription>
        </DialogHeader>

        {settings.isPending && <Skeleton className="h-24" />}
        {settings.isError && <ErrorAlert error={settings.error} />}

        <div className="flex flex-col gap-4">
          {list.map((s) => {
            const id = `plugin-setting-${s.key}`
            const why = problem(s.key)

            return (
              <div key={s.key} className="flex flex-col gap-1.5">
                {s.kind === 'bool' ? (
                  <label className="flex items-center gap-2 text-sm">
                    <Switch checked={draft(s) === true} onCheckedChange={(on) => set(s.key, on)} />
                    {s.label}
                  </label>
                ) : (
                  <Label htmlFor={id}>{s.label}</Label>
                )}

                {s.kind === 'enum' && (
                  <NativeSelect
                    id={id}
                    value={String(draft(s))}
                    onChange={(e) => set(s.key, e.target.value)}
                  >
                    {(s.options ?? []).map((o) => (
                      <NativeSelectOption key={o} value={o}>
                        {o}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                )}

                {s.kind === 'list' && (
                  <Textarea
                    id={id}
                    value={String(draft(s))}
                    onChange={(e) => set(s.key, e.target.value)}
                  />
                )}

                {['string', 'int', 'duration'].includes(s.kind) && (
                  <Input
                    id={id}
                    type={s.secret ? 'password' : s.kind === 'int' ? 'number' : 'text'}
                    autoComplete="off"
                    placeholder={
                      s.secret
                        ? s.isSet
                          ? 'Saved. Type a new value to replace it.'
                          : 'Not set'
                        : `Default: ${String(s.default ?? '')}`
                    }
                    value={String(draft(s))}
                    onChange={(e) => set(s.key, e.target.value)}
                  />
                )}

                {s.description && <p className="text-xs text-muted-foreground">{s.description}</p>}
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
          <Button onClick={submit} disabled={save.isPending || settings.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
