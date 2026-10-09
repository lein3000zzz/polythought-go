const dateTimeFormat = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'short',
})
const timeFormat = new Intl.DateTimeFormat(undefined, { timeStyle: 'medium' })

export function formatDateTime(iso: string): string {
  return dateTimeFormat.format(new Date(iso))
}

/** The time of day, with seconds: for runs, which happen many times a day. */
export function formatTime(iso: string): string {
  return timeFormat.format(new Date(iso))
}

/** 850 ms, 1.2 s, 2 min 5 s. */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`

  const minutes = Math.floor(ms / 60_000)
  const seconds = Math.round((ms % 60_000) / 1000)

  return seconds > 0 ? `${minutes} min ${seconds} s` : `${minutes} min`
}

/** 0x5aAe…eAed */
export function shortAddress(address: string): string {
  return `${address.slice(0, 6)}…${address.slice(-4)}`
}
