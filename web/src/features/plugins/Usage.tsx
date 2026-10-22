import { cn } from 'cn'
import { TriangleAlert } from 'lucide-react'

import type { PluginStatus } from '@/api/plugins'
import { formatBytes } from '@/lib/format'

/** The shares of its cap past which a meter warns, then alarms. */
const warnAt = 0.8
const alarmAt = 0.95

/** A meter's fill by how near its cap it is, its track a lighter step of the same color. */
function levelOf(share: number) {
  if (share >= alarmAt) {
    return {
      fill: 'bg-destructive',
      track: 'bg-destructive/20',
      icon: 'text-destructive',
      near: true,
    }
  }

  if (share >= warnAt) {
    return {
      fill: 'bg-amber-500',
      track: 'bg-amber-500/20',
      icon: 'text-amber-600 dark:text-amber-500',
      near: true,
    }
  }

  return { fill: 'bg-primary', track: 'bg-primary/15', icon: '', near: false }
}

function Meter({ label, used, cap }: { label: string; used: number; cap: number }) {
  const share = cap > 0 ? Math.min(used / cap, 1) : 0
  const level = levelOf(share)
  const said = `${formatBytes(used)} of ${formatBytes(cap)}`

  return (
    <>
      <span>{label}</span>
      <div
        role="meter"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={cap}
        aria-valuenow={Math.min(used, cap)}
        aria-valuetext={level.near ? `${said}, near its cap` : said}
        className={cn('h-1.5 w-full overflow-hidden rounded-full', level.track)}
      >
        <div
          className={cn('h-full rounded-full', level.fill)}
          style={{ width: `${share * 100}%` }}
        />
      </div>
      <span className="flex items-center gap-1 text-muted-foreground tabular-nums">
        {level.near && <TriangleAlert className={cn('size-3.5', level.icon)} aria-hidden />}
        {said}
        {level.near && ', near its cap'}
      </span>
    </>
  )
}

/**
 * What a plugin's process took when last measured, each against its cap (D351): its
 * memory, watched on Linux only, and its data, its directory and its temporary one.
 */
export function Usage({ status }: { status: PluginStatus }) {
  if (status.memoryCap === 0 && status.dataCap === 0) return null // it never started

  return (
    <div className="grid grid-cols-[auto_minmax(6rem,1fr)_auto] items-center gap-x-3 gap-y-1.5 text-sm">
      {status.memory > 0 ? (
        <Meter label="Memory" used={status.memory} cap={status.memoryCap} />
      ) : (
        <>
          <span>Memory</span>
          <span className="col-span-2 text-muted-foreground">
            Not measured: it is watched on Linux.
          </span>
        </>
      )}
      <Meter label="Data" used={status.data} cap={status.dataCap} />
    </div>
  )
}
