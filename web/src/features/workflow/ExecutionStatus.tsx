import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'

const labels: Record<Schemas['Execution']['status'], string> = {
  running: 'Running',
  waiting: 'Waiting',
  succeeded: 'Succeeded',
  failed: 'Failed',
  cancelled: 'Cancelled',
}

export function ExecutionStatus({ status }: { status: Schemas['Execution']['status'] }) {
  return (
    <Badge
      variant={
        status === 'failed'
          ? 'destructive'
          : status === 'succeeded'
            ? 'default'
            : status === 'waiting'
              ? 'outline'
              : 'secondary'
      }
      className="w-20 justify-center"
    >
      {labels[status]}
    </Badge>
  )
}
