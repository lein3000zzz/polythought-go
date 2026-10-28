import { useSearchParams } from 'react-router'

export type Mode = 'paper' | 'live'

export const modeLabels: Record<Mode, string> = { paper: 'Paper', live: 'Live' }

/**
 * The mode a dashboard shows (D429), and a way to change it: as the URL says, else live
 * where anything trades live.
 */
export function useMode(anyLive: boolean) {
  const [params, setParams] = useSearchParams()
  const asked = params.get('mode')
  const mode: Mode = asked === 'paper' || asked === 'live' ? asked : anyLive ? 'live' : 'paper'

  return [mode, (m: Mode) => setParams({ mode: m }, { replace: true })] as const
}
