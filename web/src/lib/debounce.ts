import { useEffect, useState } from 'react'

/** The value once it has stayed the same for ms, as a field settles while typed into. */
export function useDebounced<T>(value: T, ms: number): T {
  const [settled, setSettled] = useState(value)

  useEffect(() => {
    const t = setTimeout(() => setSettled(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])

  return settled
}
