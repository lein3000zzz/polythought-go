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

/**
 * A number that survives reloads, as useStoredFlag's boolean does, for sizes like a
 * panel's width. One saved that is not a number reads as the initial one.
 */
export function useStoredNumber(key: string, initial: number): [number, (value: number) => void] {
  const [value, setValue] = useState(() => {
    try {
      const saved = Number(localStorage.getItem(key) ?? Number.NaN)
      return Number.isFinite(saved) ? saved : initial
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
