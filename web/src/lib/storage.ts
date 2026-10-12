import { useEffect, useState } from 'react'

/**
 * A boolean that survives reloads, for layout choices like a closed panel. Storage that
 * is off or full only means the choice is not remembered.
 */
export function useStoredFlag(key: string, initial: boolean): [boolean, (value: boolean) => void] {
  const [value, setValue] = useState(() => {
    try {
      const saved = localStorage.getItem(key)
      return saved === null ? initial : saved === 'true'
    } catch {
      return initial
    }
  })

  useEffect(() => {
    try {
      localStorage.setItem(key, String(value))
    } catch {
      // not remembered
    }
  }, [key, value])

  return [value, setValue]
}
