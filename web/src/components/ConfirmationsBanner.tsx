import { ShieldAlert } from 'lucide-react'
import { Link } from 'react-router'

import { usePendingConfirmations } from '@/api/confirmations'
import { Button } from '@/components/ui/button'
import { settingsPath } from '@/features/settings/pages'

/**
 * Says, on every page, that an agent's or a token's live action waits for you (D406), and
 * takes you to where you confirm or refuse it.
 */
export function ConfirmationsBanner() {
  const pending = usePendingConfirmations().data ?? []

  if (pending.length === 0) return null

  const first = pending[0]

  return (
    <div
      role="status"
      className="flex shrink-0 items-center gap-3 border-b border-amber-500/40 bg-amber-500/10 px-4 py-2 text-sm"
    >
      <ShieldAlert className="size-4 shrink-0" />
      <span>
        <span className="font-medium">{first.asker} asks you to confirm:</span> {first.what}
        {first.about && ` (${first.about})`}
        {pending.length > 1 && `, and ${pending.length - 1} more`}.
      </span>
      <Button variant="outline" size="sm" className="ml-auto" asChild>
        <Link to={settingsPath('connected', 'confirmations')}>Review</Link>
      </Button>
    </div>
  )
}
