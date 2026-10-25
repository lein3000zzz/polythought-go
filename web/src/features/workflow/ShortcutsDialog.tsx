import { Link } from 'react-router'

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { settingsPath } from '@/features/settings/pages'

import { fixedShortcuts, shortcutGroups, shortcuts, shown, type Keymap } from './shortcuts'

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
  keymap: Keymap
}

/** Every shortcut of the editor, as the user has them (D420), by what they are for. */
export function ShortcutsDialog({ open, onOpenChange, keymap }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Keyboard shortcuts</DialogTitle>
          <DialogDescription>
            On the canvas, outside a field, where keys are the field&apos;s. Change them in{' '}
            <Link
              to={settingsPath('shortcuts')}
              className="underline underline-offset-4"
              onClick={() => onOpenChange(false)}
            >
              Settings, Shortcuts
            </Link>
            .
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-x-8 gap-y-4 sm:grid-cols-2">
          {shortcutGroups.map((group) => (
            <section key={group} aria-label={group} className="flex flex-col gap-1.5">
              <h3 className="text-xs font-medium text-muted-foreground">{group}</h3>
              <dl className="flex flex-col gap-1 text-sm">
                {[
                  ...shortcuts
                    .filter((s) => s.group === group)
                    .map((s) => ({ label: s.label, keys: keymap[s.action] })),
                  ...fixedShortcuts.filter((s) => s.group === group),
                ].map((s) => (
                  <div key={s.label} className="flex items-center justify-between gap-3">
                    <dt>{s.label}</dt>
                    <dd className="flex shrink-0 gap-1">
                      {s.keys.slice(0, 2).map((k) => (
                        <Keys key={k} binding={k} />
                      ))}
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}

/** A binding as keys on a keyboard. */
export function Keys({ binding }: { binding: string }) {
  return (
    <kbd className="rounded border bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
      {shown(binding)}
    </kbd>
  )
}
