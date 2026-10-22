import { useLayoutEffect, useState } from 'react'

/**
 * An element's size as laid out, following it as it changes; zero until it is measured.
 * It takes the element, from a callback ref, so a remounted one is followed too.
 */
export function useElementSize(el: HTMLElement | null): { width: number; height: number } {
  const [size, setSize] = useState({ width: 0, height: 0 })

  useLayoutEffect(() => {
    if (!el) return

    const observer = new ResizeObserver(([entry]) => {
      const { width, height } = entry.contentRect
      setSize((s) => (s.width === width && s.height === height ? s : { width, height }))
    })
    observer.observe(el)

    return () => observer.disconnect()
  }, [el])

  return size
}
