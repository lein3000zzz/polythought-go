import { createContext, useContext } from 'react'

import type { Problem } from '@/api/client'

/** What the server found wrong with the workflow when it was last saved. */
export const ProblemsContext = createContext<readonly Problem[]>([])

export function useNodeProblems(nodeId: string): Problem[] {
  return useContext(ProblemsContext).filter((p) => p.nodeId === nodeId)
}
