import { createContext, useContext } from 'react'

import type { Schemas } from '@/api/client'

export type NodeRun = Schemas['NodeRun']

/** The node runs of the execution being inspected, by node ID; undefined when none is. */
export const RunsContext = createContext<ReadonlyMap<string, NodeRun> | undefined>(undefined)

export function useNodeRun(nodeId: string): NodeRun | undefined {
  return useContext(RunsContext)?.get(nodeId)
}
