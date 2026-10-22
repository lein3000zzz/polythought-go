import { useState } from 'react'
import { toast } from 'sonner'

import { type Plugin, useApprovePlugin } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { cn } from 'cn'

import { always, brings, describe } from './permissions'
import { signedBy } from './signer'

type Props = {
  plugin: Plugin
  version: string
  onClose: () => void
}

/**
 * The permissions screen (D331): what a version brings, who signed it, and each
 * permission it asks for in words, ticked one by one before it may be approved. An
 * upgrade's permissions the running version was not approved for are marked new.
 */
export function ApproveDialog({ plugin, version, onClose }: Props) {
  const approve = useApprovePlugin()
  const [ticked, setTicked] = useState<Set<string>>(new Set())
  const [warned, setWarned] = useState(false)

  const v = plugin.versions.find((pv) => pv.version === version)
  if (!v) return null

  const running = plugin.versions.find((pv) => pv.version === plugin.version)
  const what = brings(v.manifest)
  const asked = v.permissions.map(describe)
  const signer = signedBy(v)
  const allTicked = asked.every((p) => ticked.has(p.name)) && (signer.trusted || warned)

  const tick = (name: string, on: boolean) =>
    setTicked((was) => {
      const next = new Set(was)
      if (on) next.add(name)
      else next.delete(name)

      return next
    })

  const submit = () =>
    approve.mutate(
      { id: plugin.id, version, permissions: v.permissions },
      {
        onSuccess: () => {
          toast.success(`${plugin.name} ${version} is approved: it runs once the server restarts`)
          onClose()
        },
      },
    )

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>
            Approve {plugin.name} {version}
          </DialogTitle>
          <DialogDescription>
            {plugin.id}
            {running ? `, replacing ${running.version}` : ', new here'}. {what.description}
          </DialogDescription>
        </DialogHeader>

        {signer.trusted ? (
          <p className="text-sm">{signer.text}.</p>
        ) : (
          <Alert variant="destructive">
            <AlertTitle>{signer.text}</AlertTitle>
            <AlertDescription className="flex flex-col gap-2">
              <p>
                Nobody you trust vouches for what it is. Approve it only if you know where it came
                from.
              </p>
              <label className="flex items-center gap-2 text-sm text-foreground">
                <Checkbox checked={warned} onCheckedChange={(on) => setWarned(on === true)} />I know
                where it came from
              </label>
            </AlertDescription>
          </Alert>
        )}

        <section className="flex flex-col gap-1 text-sm">
          <h3 className="font-medium">It brings</h3>
          <ul className="list-disc pl-5 text-muted-foreground">
            {what.nodes.length > 0 && <li>Nodes: {what.nodes.join(', ')}</li>}
            {what.triggers.length > 0 && <li>Triggers: {what.triggers.join(', ')}</li>}
            {what.credentialTypes.length > 0 && (
              <li>Credential types: {what.credentialTypes.join(', ')}</li>
            )}
            {what.scenarios.length > 0 && <li>Scenarios: {what.scenarios.join(', ')}</li>}
          </ul>
        </section>

        <section className="flex flex-col gap-2 text-sm">
          <h3 className="font-medium">It asks to</h3>
          {asked.length === 0 && <p className="text-muted-foreground">Nothing more.</p>}
          {asked.map((p) => {
            const isNew = running !== undefined && !running.permissions.includes(p.name)

            return (
              <label
                key={p.name}
                className={cn(
                  'flex items-start gap-2 rounded-md border p-3',
                  p.risky && 'border-destructive/50 bg-destructive/5',
                )}
              >
                <Checkbox
                  className="mt-0.5"
                  aria-label={p.title}
                  checked={ticked.has(p.name)}
                  onCheckedChange={(on) => tick(p.name, on === true)}
                />
                <span className="flex flex-col gap-0.5">
                  <span className="flex flex-wrap items-center gap-1.5 font-medium">
                    {p.title}
                    {p.risky && <Badge variant="destructive">Take care</Badge>}
                    {isNew && <Badge variant="outline">New</Badge>}
                  </span>
                  <span className="text-muted-foreground">{p.detail}</span>
                  <code className="text-xs text-muted-foreground">{p.name}</code>
                </span>
              </label>
            )
          })}
        </section>

        <section className="flex flex-col gap-1 text-sm">
          <h3 className="font-medium">Every plugin may</h3>
          <ul className="list-disc pl-5 text-muted-foreground">
            {always.map((a) => (
              <li key={a}>{a}</li>
            ))}
          </ul>
        </section>

        {approve.error && <ErrorAlert error={approve.error} />}

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!allTicked || approve.isPending}>
            Approve
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
