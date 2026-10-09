import { createContext, useContext } from 'react'

import type { Problem } from '@/api/client'

/** What the server found wrong with the workflow when it was last saved. */
export const ProblemsContext = createContext<readonly Problem[]>([])

export function useNodeProblems(nodeId: string): Problem[] {
  return useContext(ProblemsContext).filter((p) => p.nodeId === nodeId)
}

/**
 * A problem's message without the 'node "x": param "y"' the server starts it with, for
 * showing next to that node's field.
 */
export function fieldMessage(p: Problem): string {
  const message = p.message.replace(/^node "[^"]*":\s*(param "[^"]*"\s*)?/, '')
  return message.charAt(0).toUpperCase() + message.slice(1)
}
