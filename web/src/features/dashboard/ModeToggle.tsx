import { Button } from '@/components/ui/button'

import { type Mode, modeLabels } from './mode'

/** Paper or live, for a dashboard: the two are never added together. */
export function ModeToggle({ mode, onChange }: { mode: Mode; onChange: (m: Mode) => void }) {
  return (
    <div role="group" aria-label="Mode" className="flex gap-1">
      {(Object.keys(modeLabels) as Mode[]).map((m) => (
        <Button
          key={m}
          size="sm"
          variant={mode === m ? 'secondary' : 'ghost'}
          aria-pressed={mode === m}
          onClick={() => onChange(m)}
        >
          {modeLabels[m]}
        </Button>
      ))}
    </div>
  )
}
