import { useEffect } from 'react'
import { useLocation } from 'react-router'

/**
 * A link to #<key> on a page shows the element with id prefix + key, when key is one of
 * keys: scrolled to, its first field focused, highlighted for a moment (D391). Keys that
 * load later jump when they come. Following the same link again shows it again.
 */
export function useJump(prefix: string, keys: string[]) {
  const location = useLocation()
  const target = decodeURIComponent(location.hash.slice(1))
  const known = keys.includes(target)

  useEffect(() => {
    if (!known) return

    const el = document.getElementById(prefix + target)
    el?.scrollIntoView({ block: 'center' })
    el?.querySelector<HTMLElement>('input, select, textarea, button, [role="switch"]')?.focus({
      preventScroll: true,
    })
    el?.animate?.(
      [{ backgroundColor: 'color-mix(in oklab, var(--color-amber-500) 20%, transparent)' }, {}],
      { duration: 1500, easing: 'ease-in' },
    )
  }, [known, prefix, target, location.key])
}
