import { useEffect, useState } from 'react'
import { toast } from 'sonner'

import { useMe, useSetShortcuts } from '@/api/auth'
import { errorMessage } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Keys } from '@/features/workflow/ShortcutsDialog'
import {
  bindingOf,
  bindingProblem,
  fixedShortcuts,
  keymapOf,
  shortcutGroups,
  shortcuts,
  takenBy,
  type Shortcut,
  type ShortcutAction,
} from '@/features/workflow/shortcuts'

import { jumpId } from './pages'

/**
 * The workflow editor's shortcuts, as the user has them (D420): each changed by pressing
 * its new keys, kept for their account, and put back to its keys to start with.
 */
export function ShortcutsSection() {
  const me = useMe()
  const set = useSetShortcuts()
  const own = me.data?.shortcuts ?? {}
  const keymap = keymapOf(own)
  const [changing, setChanging] = useState<ShortcutAction>()

  const keep = (next: Record<string, string>, said: string) =>
    set.mutate(next, {
      onSuccess: () => toast.success(said),
      onError: (error) => toast.error(errorMessage(error)),
    })

  return (
    <section id={jumpId('shortcuts')} className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h2 className="font-medium">Workflow editor shortcuts</h2>
          <p className="text-sm text-muted-foreground">
            The keys the canvas takes, outside a field. Press Change, then the new keys; Esc leaves
            them as they are. ? in the editor lists them.
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          disabled={Object.keys(own).length === 0 || set.isPending}
          onClick={() => keep({}, 'Every shortcut is back to its keys')}
        >
          Reset all
        </Button>
      </div>

      {shortcutGroups.map((group) => (
        <div key={group} className="flex flex-col gap-1">
          <h3 className="text-xs font-medium text-muted-foreground">{group}</h3>
          <ul aria-label={group} className="flex flex-col divide-y rounded-md border text-sm">
            {shortcuts
              .filter((s) => s.group === group)
              .map((s) => (
                <li key={s.action} className="flex flex-wrap items-center gap-3 px-3 py-2">
                  <span className="min-w-0 flex-1">{s.label}</span>
                  {changing === s.action ? (
                    <Capture
                      shortcut={s}
                      onDone={(binding) => {
                        setChanging(undefined)
                        if (binding === undefined) return

                        const taken = takenBy(binding, s.action, keymap)
                        if (taken) {
                          toast.error(
                            `Those keys are “${taken.label}”'s already: change that one first`,
                          )
                          return
                        }

                        keep({ ...own, [s.action]: binding }, `${s.label}: changed`)
                      }}
                    />
                  ) : (
                    <span className="flex gap-1">
                      {keymap[s.action].map((k) => (
                        <Keys key={k} binding={k} />
                      ))}
                    </span>
                  )}
                  <span className="flex gap-1">
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={set.isPending}
                      onClick={() => setChanging(changing === s.action ? undefined : s.action)}
                    >
                      Change
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={!own[s.action] || set.isPending}
                      onClick={() => {
                        const { [s.action]: _, ...rest } = own
                        keep(rest, `${s.label}: back to its keys`)
                      }}
                    >
                      Reset
                    </Button>
                  </span>
                </li>
              ))}
            {fixedShortcuts
              .filter((s) => s.group === group)
              .map((s) => (
                <li
                  key={s.label}
                  className="flex flex-wrap items-center gap-3 px-3 py-2 text-muted-foreground"
                >
                  <span className="min-w-0 flex-1">{s.label}</span>
                  <span className="flex gap-1">
                    {s.keys.slice(0, 2).map((k) => (
                      <Keys key={k} binding={k} />
                    ))}
                  </span>
                  <span className="text-xs">Fixed</span>
                </li>
              ))}
          </ul>
        </div>
      ))}
    </section>
  )
}

/** Waits for the keys to give a shortcut; Esc gives none. */
function Capture({ shortcut, onDone }: { shortcut: Shortcut; onDone: (binding?: string) => void }) {
  const [problem, setProblem] = useState<string>()

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      e.preventDefault()
      e.stopPropagation()

      if (e.key === 'Escape' && !e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey) {
        onDone(undefined)
        return
      }

      const binding = bindingOf(e)
      if (!binding) return // a modifier alone, so far

      const why = bindingProblem(binding)
      if (why) {
        setProblem(why)
        return
      }

      onDone(binding)
    }

    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [onDone])

  return (
    <span
      role="status"
      aria-label={`New keys for ${shortcut.label}`}
      className="flex flex-col text-xs"
    >
      <span className="rounded border border-dashed px-2 py-0.5">Press the new keys…</span>
      {problem && <span className="text-destructive">{problem}</span>}
    </span>
  )
}
