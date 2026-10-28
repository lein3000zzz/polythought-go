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

const byteUnits = ['B', 'KiB', 'MiB', 'GiB', 'TiB']

/** 512 KiB, 1.5 MiB, 40 MiB, 1 GiB: sizes as plugins' caps count them. */
export function formatBytes(n: number): string {
  let v = n
  let unit = 0

  while (v >= 1024 && unit < byteUnits.length - 1) {
    v /= 1024
    unit++
  }

  const text = v >= 10 || Number.isInteger(v) ? Math.round(v).toString() : v.toFixed(1)

  return `${text} ${byteUnits[unit]}`
}

/** 999, 1.2k, 12k, 1.2m: a count as GitHub writes it, cut down, never rounded up. */
export function formatCount(n: number): string {
  if (n < 1000) return n.toString()

  const [size, unit] = n < 1_000_000 ? [1000, 'k'] : [1_000_000, 'm']
  const tenths = Math.floor(n / (size / 10))
  const text = tenths < 100 ? (tenths / 10).toString() : Math.floor(tenths / 10).toString()

  return text + unit
}

/** 0x5aAe…eAed */
export function shortAddress(address: string): string {
  return `${address.slice(0, 6)}…${address.slice(-4)}`
}

const dollarFormat = new Intl.NumberFormat(undefined, { style: 'currency', currency: 'USD' })
const shareFormat = new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 })

/** $1,012.40, from a decimal's text. */
export function formatDollars(text: string): string {
  return dollarFormat.format(Number(text))
}

/** +$12.40 or −$1.20: a gain or a loss. */
export function formatProfit(text: string): string {
  const n = Number(text)
  const sign = n > 0 ? '+' : n < 0 ? '−' : ''

  return sign + dollarFormat.format(Math.abs(n))
}

/** 19.8 shares, as many decimals as there are, up to 2. */
export function formatShares(text: string): string {
  return shareFormat.format(Number(text))
}

/**
 * How long ago a time was, as a card says it: just now, 5 min ago, 3 h ago, 2 d ago; the
 * date past a week.
 */
export function formatAgo(iso: string, now: number = Date.now()): string {
  const minutes = Math.floor((now - Date.parse(iso)) / 60_000)

  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes} min ago`
  if (minutes < 24 * 60) return `${Math.floor(minutes / 60)} h ago`
  if (minutes < 7 * 24 * 60) return `${Math.floor(minutes / (24 * 60))} d ago`

  return formatDateTime(iso)
}
