import { useState } from 'react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { ApiError, errorMessage } from '@/api/client'
import { useUpdateSettings, type Setting } from '@/api/settings'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { useJump } from '@/lib/jump'

import { CodeRunners } from './CodeRunners'
import { MailTest } from './MailTest'
import { configPath, isChange, rowId, settingPrefix, type ConfigPage, type Drafts } from './pages'
import { SettingRow } from './SettingRow'

const mailGroup = 'Mail'

const codeGroup = 'Code'

const tradingGroup = 'Trading'

const liveTrading = 'trading.live'

type Props = {
  page: ConfigPage
  byKey: Map<string, Setting>
  drafts: Drafts
  // Bumped to remount fields, so they show saved values again instead of what was typed.
  version: number
  resets: Record<string, number>
  onEdit: (key: string, value: unknown) => void
  onReset: (key: string) => void
  onClear: (keys: string[]) => void
}

/**
 * A group's settings, saving its own changes (D370): drafts of the other pages wait on
 * theirs. A link to #<key> shows the setting (useJump).
 */
export function GroupPage({
  page,
  byKey,
  drafts,
  version,
  resets,
  onEdit,
  onReset,
  onClear,
}: Props) {
  const update = useUpdateSettings()
  const [askLiveOff, setAskLiveOff] = useState(false)
  const keys = page.settings.map((s) => s.key)

  useJump(settingPrefix, keys)

  const changes = Object.fromEntries(
    page.settings
      .filter((s) => s.key in drafts && isChange(s, drafts[s.key]))
      .map((s) => [s.key, drafts[s.key]]),
  )
  const count = Object.keys(changes).length

  const problems = new Map(
    update.error instanceof ApiError
      ? update.error.problems.flatMap((p) => (p.param ? [[p.param, p.message] as const] : []))
      : [],
  )

  const clear = () => {
    onClear(keys)
    update.reset()
  }

  const store = () =>
    update.mutate(changes, {
      onSuccess: (data) => {
        clear()
        toast.success(
          data.restartPending ? 'Saved. Some changes apply after a restart.' : 'Settings saved',
        )
      },
    })

  // Turning live trading off cancels every live order resting on the book (D226): asked
  // first. A reset goes back to its default, off.
  const turnsLiveOff =
    byKey.get(liveTrading)?.value === true &&
    liveTrading in changes &&
    (changes[liveTrading] === false || changes[liveTrading] === null)

  const save = () => (turnsLiveOff ? setAskLiveOff(true) : store())

  return (
    <div className="flex flex-col gap-6">
      <section className="flex flex-col gap-5 rounded-lg border p-4">
        <h2 className="font-medium">{page.label}</h2>
        {page.label === tradingGroup && (
          <p className="-mt-3 text-sm text-muted-foreground">
            Whether Polymarket takes orders from this server is on the{' '}
            <Link to={configPath()} className="text-foreground underline underline-offset-4">
              overview
            </Link>
            .
          </p>
        )}

        {page.settings.map((s) => (
          <div
            key={`${s.key}-${version}-${resets[s.key] ?? 0}`}
            id={rowId(s.key)}
            className="-m-2 rounded-md p-2"
          >
            <SettingRow
              setting={s}
              draft={drafts[s.key]}
              hasDraft={s.key in drafts}
              problem={problems.get(s.key)}
              onChange={(value) => onEdit(s.key, value)}
              onReset={() => onReset(s.key)}
            />
          </div>
        ))}

        {page.label === mailGroup && <MailTest settings={byKey} />}
        {page.label === codeGroup && <CodeRunners />}
      </section>

      {(count > 0 || update.isError) && (
        <div className="sticky bottom-0 flex flex-wrap items-center justify-end gap-3 rounded-lg border bg-background/95 px-4 py-3 backdrop-blur">
          <span className="mr-auto text-sm text-muted-foreground">
            {update.isError && problems.size === 0
              ? errorMessage(update.error)
              : `${count} unsaved ${count === 1 ? 'change' : 'changes'}`}
          </span>
          <Button variant="ghost" onClick={clear}>
            Discard
          </Button>
          <Button onClick={save} disabled={count === 0 || update.isPending}>
            Save
          </Button>
        </div>
      )}

      <ConfirmDialog
        open={askLiveOff}
        title="Turn live trading off?"
        description="Every live order resting on the book is cancelled, every user's, and workflows place no live orders until it is on again. What the orders filled before stays."
        confirm="Turn off and cancel"
        destructive
        onCancel={() => setAskLiveOff(false)}
        onConfirm={() => {
          setAskLiveOff(false)
          store()
        }}
      />
    </div>
  )
}
