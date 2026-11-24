import { Copy, KeyRound, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { useMe } from '@/api/auth'
import { errorMessage, type Schemas } from '@/api/client'
import {
  useCreateMLModel,
  useCreateWorkerKey,
  useDeleteMLModel,
  useDeleteWorkerKey,
  useMLModels,
  useWorkerKeys,
} from '@/api/mlmodels'
import { useCredentialList } from '@/api/vault'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatDateTime } from '@/lib/format'

type Model = Schemas['MLModel']

/**
 * Your models: served by workers on your own machines, which take their requests, or
 * called at a URL. Each with its workers connected now, its last answer and error, its
 * worker keys, and delete.
 */
export function ModelsPage() {
  const me = useMe()
  const list = useMLModels()
  const remove = useDeleteMLModel()
  const [making, setMaking] = useState(false)
  const [open, setOpen] = useState<string>()
  const [deleting, setDeleting] = useState<Model>()
  const models = list.data?.models ?? []

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold">Models</h2>
          <p className="text-sm text-muted-foreground">
            Workers on your own machines serve them, or they answer at a URL. Workflows ask them.{' '}
            <a className="underline" href="/docs/guides/models/" target="_blank" rel="noopener">
              How they work
            </a>
          </p>
        </div>
        <Button size="sm" onClick={() => setMaking(true)} disabled={!list.isSuccess}>
          <Plus />
          New model
        </Button>
      </div>

      {me.data && !me.data.mayUseData && (
        <p className="text-sm text-muted-foreground">
          Models are not open to you here: an admin decides who may use them.
        </p>
      )}

      {list.isPending && <Skeleton className="h-32" />}
      {list.isError && <ErrorAlert error={list.error} onRetry={() => void list.refetch()} />}

      {list.isSuccess && models.length === 0 && (
        <p className="text-sm text-muted-foreground">
          None yet. Make one, then a key for its workers.
        </p>
      )}

      {models.length > 0 && (
        <Table aria-label="Models">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Served by</TableHead>
              <TableHead>Last answer</TableHead>
              <TableHead>Last error</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {models.map((m) => (
              <TableRow key={m.name}>
                <TableCell className="font-medium">{m.name}</TableCell>
                <TableCell className="text-muted-foreground">{servedBy(m)}</TableCell>
                <TableCell className="text-muted-foreground">
                  {m.lastAnswerAt ? formatDateTime(m.lastAnswerAt) : 'Never'}
                </TableCell>
                <TableCell className="max-w-56 truncate text-muted-foreground" title={m.lastError}>
                  {m.lastError
                    ? `${m.lastError}${m.lastErrorAt ? `, ${formatDateTime(m.lastErrorAt)}` : ''}`
                    : '—'}
                </TableCell>
                <TableCell className="flex justify-end gap-1">
                  {m.kind === 'worker' && (
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-expanded={open === m.name}
                      onClick={() => setOpen(open === m.name ? undefined : m.name)}
                    >
                      <KeyRound />
                      Keys
                    </Button>
                  )}
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`Delete ${m.name}`}
                    onClick={() => setDeleting(m)}
                  >
                    <Trash2 />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {open && <Keys model={open} />}

      {making && list.data && (
        <NewModelDialog
          limits={list.data.limits}
          onClose={() => setMaking(false)}
          onMade={(name, kind) => {
            setMaking(false)
            if (kind === 'worker') setOpen(name)
          }}
        />
      )}

      <ConfirmDialog
        open={deleting !== undefined}
        title={`Delete ${deleting?.name}?`}
        description={deleting ? deleteNote(deleting) : ''}
        confirm="Delete"
        destructive
        onCancel={() => setDeleting(undefined)}
        onConfirm={() => {
          const name = deleting?.name
          if (!name) return

          remove.mutate(name, {
            onSuccess: () => {
              toast.success(`Deleted ${name}`)
              if (open === name) setOpen(undefined)
            },
            onError: (error) => toast.error(errorMessage(error)),
          })
        }}
      />
    </div>
  )
}

/** What deleting a model takes with it, and the workflows it switches off. */
function deleteNote(m: Model): string {
  const gone =
    m.kind === 'worker'
      ? 'Its keys go with it, and its workers are refused from their next call.'
      : 'Workflows can no longer ask it.'
  const using = m.workflows ?? []
  if (using.length === 0) return gone

  const names = using.map((w) => `“${w.name}”${w.active ? ' (on)' : ''}`)
  const listed =
    names.length === 1 ? names[0] : `${names.slice(0, -1).join(', ')} and ${names.at(-1)}`
  const on = using.some((w) => w.active)

  return `${gone} ${listed} ${using.length === 1 ? 'asks' : 'ask'} it${on ? ': those on are switched off' : ''}.`
}

/** Who serves a model: its workers connected now, or its URL's host. */
function servedBy(m: Model): string {
  if (m.kind === 'url') {
    try {
      return `URL: ${new URL(m.url ?? '').host}`
    } catch {
      return 'URL'
    }
  }

  if (m.workers.length === 0) return 'No worker connected'

  const names = m.workers.map((w) => (w.version ? `${w.name} (${w.version})` : w.name))
  return `${m.workers.length} connected: ${names.join(', ')}`
}

/** A worker model's keys: a new one's secret shown once, the others revoked. */
function Keys({ model }: { model: string }) {
  const keys = useWorkerKeys(model)
  const create = useCreateWorkerKey(model)
  const revoke = useDeleteWorkerKey(model)
  const [name, setName] = useState('')
  const [made, setMade] = useState<Schemas['NewWorkerKey']>()

  return (
    <section
      aria-label={`${model}'s worker keys`}
      className="flex flex-col gap-3 rounded-lg border p-4"
    >
      <h3 className="text-sm font-medium">{model}: worker keys</h3>
      <p className="text-sm text-muted-foreground">
        A worker serves this model alone with one of its keys, sent as{' '}
        <code className="font-mono text-xs">Authorization: Bearer pw_…</code>.
      </p>

      {made && (
        <Alert>
          <KeyRound />
          <AlertTitle>Copy the key “{made.key.name}” now</AlertTitle>
          <AlertDescription className="flex flex-col gap-2">
            <p>It is shown only this once.</p>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 font-mono text-xs">
                {made.secret}
              </code>
              <Button
                variant="outline"
                size="sm"
                onClick={() =>
                  void navigator.clipboard
                    .writeText(made.secret)
                    .then(() => toast.success('Key copied'))
                }
              >
                <Copy />
                Copy
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setMade(undefined)}>
                Done
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      )}

      <form
        className="flex items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          create.mutate(name.trim(), {
            onSuccess: (k) => {
              setMade(k)
              setName('')
            },
            onError: (error) => toast.error(errorMessage(error)),
          })
        }}
      >
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={`${model}-key-name`}>New key</Label>
          <Input
            id={`${model}-key-name`}
            value={name}
            placeholder="gpu-box"
            maxLength={64}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <Button type="submit" size="sm" disabled={!name.trim() || create.isPending}>
          Make key
        </Button>
      </form>

      {keys.isPending && <Skeleton className="h-16" />}
      {keys.isError && <ErrorAlert error={keys.error} onRetry={() => void keys.refetch()} />}
      {keys.isSuccess && keys.data.keys.length === 0 && (
        <p className="text-sm text-muted-foreground">No keys yet.</p>
      )}
      {keys.isSuccess && keys.data.keys.length > 0 && (
        <Table aria-label={`${model}'s keys`}>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Starts</TableHead>
              <TableHead>Made</TableHead>
              <TableHead>Last used</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {keys.data.keys.map((k) => (
              <TableRow key={k.id}>
                <TableCell>{k.name}</TableCell>
                <TableCell className="font-mono text-xs">{k.prefix}…</TableCell>
                <TableCell className="text-muted-foreground">
                  {formatDateTime(k.createdAt)}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {k.lastUsedAt ? formatDateTime(k.lastUsedAt) : 'Never'}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={`Revoke ${k.name}`}
                    onClick={() =>
                      revoke.mutate(k.id, {
                        onSuccess: () => toast.success(`Revoked ${k.name}`),
                        onError: (error) => toast.error(errorMessage(error)),
                      })
                    }
                  >
                    Revoke
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  )
}

type NewModelProps = {
  limits: Schemas['MLModelLimits']
  onMade: (name: string, kind: Model['kind']) => void
  onClose: () => void
}

/** Makes a model: served by workers, or at a URL with an optional header credential. */
function NewModelDialog({ limits, onMade, onClose }: NewModelProps) {
  const create = useCreateMLModel()
  const credentials = useCredentialList()
  const [name, setName] = useState('')
  const [kind, setKind] = useState<Model['kind']>('worker')
  const [url, setUrl] = useState('')
  const [credentialId, setCredentialId] = useState('')
  const headers = credentials.data?.filter((c) => c.type === 'http.header') ?? []

  return (
    <Dialog open onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New model</DialogTitle>
          <DialogDescription>
            Yours alone, by its name, which never changes. You may have{' '}
            {limits.models.toLocaleString('en-US')}.
          </DialogDescription>
        </DialogHeader>

        <form
          id="new-model"
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault()
            create.mutate(
              {
                name: name.trim(),
                kind,
                ...(kind === 'url'
                  ? { url: url.trim(), ...(credentialId ? { credentialId } : {}) }
                  : {}),
              },
              { onSuccess: (m) => onMade(m.name, m.kind) },
            )
          }}
        >
          <div className="flex flex-col gap-2">
            <Label htmlFor="new-model-name">Name</Label>
            <Input
              id="new-model-name"
              value={name}
              required
              maxLength={64}
              pattern="[a-z0-9][a-z0-9_\-]*"
              placeholder="temperature-model"
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <fieldset className="flex flex-col gap-2">
            <legend className="mb-2 text-sm font-medium">Served by</legend>
            <label className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name="new-model-kind"
                checked={kind === 'worker'}
                onChange={() => setKind('worker')}
              />
              <span>
                Workers
                <span className="block text-muted-foreground">
                  On your own machines: they call in for its requests, with a key of its own.
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name="new-model-kind"
                checked={kind === 'url'}
                onChange={() => setKind('url')}
              />
              <span>
                A URL
                <span className="block text-muted-foreground">
                  Called with the items, as webhooks are, and answering with JSON.
                </span>
              </span>
            </label>
          </fieldset>
          {kind === 'url' && (
            <>
              <div className="flex flex-col gap-2">
                <Label htmlFor="new-model-url">URL</Label>
                <Input
                  id="new-model-url"
                  type="url"
                  required
                  value={url}
                  placeholder="https://models.example.com/predict"
                  onChange={(e) => setUrl(e.target.value)}
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="new-model-credential">Credential</Label>
                <NativeSelect
                  id="new-model-credential"
                  className="w-full"
                  value={credentialId}
                  onChange={(e) => setCredentialId(e.target.value)}
                >
                  <NativeSelectOption value="">None</NativeSelectOption>
                  {headers.map((c) => (
                    <NativeSelectOption key={c.id} value={c.id}>
                      {c.name}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <p className="text-xs text-muted-foreground">
                  An HTTP header credential, sent with each call.
                </p>
              </div>
            </>
          )}
          {create.isError && (
            <p role="alert" className="text-sm text-destructive">
              {errorMessage(create.error)}
            </p>
          )}
        </form>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="new-model" disabled={!name.trim() || create.isPending}>
            Make it
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
