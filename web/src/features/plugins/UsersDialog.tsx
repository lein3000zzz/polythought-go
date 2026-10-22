import { useState } from 'react'
import { toast } from 'sonner'

import { useUsers } from '@/api/admin'
import { type Plugin, useSetPluginUsers } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
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
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'

type Props = {
  plugin: Plugin
  onClose: () => void
}

/**
 * Who may use a plugin (D282): everyone, or the users chosen. It applies at once: the
 * active workflows of those who no longer may are paused, and they are told (D319).
 */
export function UsersDialog({ plugin, onClose }: Props) {
  const users = useUsers()
  const save = useSetPluginUsers()
  const [everyone, setEveryone] = useState(plugin.everyone)
  const [chosen, setChosen] = useState<Set<string>>(new Set(plugin.users))
  const [filter, setFilter] = useState('')

  const all = users.data?.pages.flatMap((page) => page.users) ?? []
  const shown = all.filter((u) => u.email.toLowerCase().includes(filter.trim().toLowerCase()))

  const choose = (id: string, on: boolean) =>
    setChosen((was) => {
      const next = new Set(was)
      if (on) next.add(id)
      else next.delete(id)

      return next
    })

  const submit = () =>
    save.mutate(
      { id: plugin.id, everyone, users: everyone ? [] : [...chosen] },
      {
        onSuccess: () => {
          toast.success(
            everyone
              ? `Everyone may use ${plugin.name}`
              : `${chosen.size} chosen users may use ${plugin.name}`,
          )
          onClose()
        },
      },
    )

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Who may use {plugin.name}</DialogTitle>
          <DialogDescription>
            Applies at once: the active workflows of anyone who may no longer use it are paused, and
            they are told.
          </DialogDescription>
        </DialogHeader>

        <label className="flex items-center gap-2 text-sm">
          <Switch checked={everyone} onCheckedChange={setEveryone} />
          Everyone
        </label>

        {!everyone && (
          <div className="flex flex-col gap-2">
            <Input
              aria-label="Find a user"
              placeholder="Find a user"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            <ul className="flex max-h-72 flex-col gap-1 overflow-y-auto">
              {shown.map((u) => (
                <li key={u.id}>
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={chosen.has(u.id)}
                      onCheckedChange={(on) => choose(u.id, on === true)}
                    />
                    {u.email}
                  </label>
                </li>
              ))}
            </ul>
            {users.hasNextPage && (
              <Button
                variant="outline"
                size="sm"
                onClick={() => void users.fetchNextPage()}
                disabled={users.isFetchingNextPage}
              >
                More users
              </Button>
            )}
          </div>
        )}

        {save.error && <ErrorAlert error={save.error} />}

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
