import { CircleAlert, KeyRound, Pencil, Plus, Trash2 } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { toast } from 'sonner'

import { ApiError, errorMessage } from '@/api/client'
import {
  useCreateCredential,
  useCredentialList,
  useCredentialTypes,
  useDeleteCredential,
  useTestCredential,
  useUpdateCredential,
  type Credential,
  type CredentialType,
} from '@/api/vault'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
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

import { jumpId } from './pages'

/** The credentials page of Settings (D392), which was a page of its own. */
export function Credentials() {
  const list = useCredentialList()
  const types = useCredentialTypes()
  const [creating, setCreating] = useState(false)

  return (
    <section className="flex flex-col gap-6">
      <div id={jumpId('new-credential')} className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h2 className="font-medium">Credentials</h2>
          <p className="text-sm text-muted-foreground">
            Keys and tokens your workflows use, such as a header a webhook sends. Secret values are
            stored encrypted and never shown again. Only your own workflows can use them, and
            sharing a workflow leaves them out.
          </p>
        </div>
        <Button onClick={() => setCreating(true)} disabled={!types.data?.length}>
          <Plus />
          New credential
        </Button>
      </div>

      {(list.isPending || types.isPending) && <Skeleton className="h-32" />}
      {list.isError && <ErrorAlert error={list.error} onRetry={() => void list.refetch()} />}
      {types.isError && <ErrorAlert error={types.error} onRetry={() => void types.refetch()} />}
      {list.data?.length === 0 && (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
          No credentials yet.
        </p>
      )}

      {list.data && list.data.length > 0 && types.data && (
        <CredentialTable credentials={list.data} types={types.data} />
      )}

      {creating && types.data && (
        <CredentialDialog types={types.data} onClose={() => setCreating(false)} />
      )}
    </section>
  )
}

function CredentialTable({
  credentials,
  types,
}: {
  credentials: Credential[]
  types: CredentialType[]
}) {
  const test = useTestCredential()
  const remove = useDeleteCredential()
  const [editing, setEditing] = useState<Credential>()
  const [deleting, setDeleting] = useState<Credential>()
  const byName = new Map(types.map((t) => [t.name, t]))

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead>Values</TableHead>
            <TableHead>Last test</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {credentials.map((c) => {
            const type = byName.get(c.type)

            return (
              <TableRow key={c.id} id={jumpId(`credential-${c.id}`)}>
                <TableCell className="font-medium">{c.name}</TableCell>
                <TableCell>{type?.label ?? c.type}</TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-1.5">
                    {type?.fields.map((f) =>
                      f.secret
                        ? c.secrets.includes(f.name) && (
                            <Badge key={f.name} variant="secondary">
                              <KeyRound />
                              {f.label} saved
                            </Badge>
                          )
                        : c.values[f.name] && (
                            <Badge key={f.name} variant="outline">
                              {f.label}: {c.values[f.name]}
                            </Badge>
                          ),
                    )}
                  </div>
                </TableCell>
                <TableCell className="text-sm">
                  {!c.testedAt ? (
                    <span className="text-muted-foreground">
                      {type?.testable ? 'Not tested' : '—'}
                    </span>
                  ) : c.testError ? (
                    <span className="text-destructive">Failed: {c.testError}</span>
                  ) : (
                    <span>Passed {formatDateTime(c.testedAt)}</span>
                  )}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex justify-end gap-1">
                    {type?.testable && (
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={test.isPending}
                        onClick={() =>
                          test.mutate(c.id, {
                            onSuccess: (result) =>
                              result.testError
                                ? toast.error(`${c.name}: ${result.testError}`)
                                : toast.success(`${c.name} works`),
                            onError: (error) => toast.error(errorMessage(error)),
                          })
                        }
                      >
                        Test
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      aria-label={`Edit ${c.name}`}
                      disabled={!type}
                      onClick={() => setEditing(c)}
                    >
                      <Pencil />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-sm"
                      className="text-destructive"
                      aria-label={`Delete ${c.name}`}
                      onClick={() => setDeleting(c)}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>

      {editing && (
        <CredentialDialog
          types={types}
          credential={editing}
          onClose={() => setEditing(undefined)}
        />
      )}

      <ConfirmDialog
        open={deleting !== undefined}
        title={`Delete ${deleting?.name}?`}
        description="Workflows that use it fail until you pick another credential in their settings."
        confirm="Delete"
        destructive
        onCancel={() => setDeleting(undefined)}
        onConfirm={() => {
          const c = deleting
          setDeleting(undefined)
          if (!c) return

          remove.mutate(c.id, {
            onSuccess: () => toast.success(`Deleted ${c.name}`),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }}
      />
    </>
  )
}

type Form = { type: string; name: string; values: Record<string, string> }

/** Creates a credential, or edits one when given. */
function CredentialDialog({
  types,
  credential,
  onClose,
}: {
  types: CredentialType[]
  credential?: Credential
  onClose: () => void
}) {
  const create = useCreateCredential()
  const update = useUpdateCredential()
  const save = credential ? update : create
  const { register, handleSubmit, control } = useForm<Form>({
    defaultValues: {
      type: credential?.type ?? types[0]?.name,
      name: credential?.name ?? '',
      values: { ...credential?.values },
    },
  })

  const typeName = useWatch({ control, name: 'type' })
  const type = types.find((t) => t.name === typeName)
  const problems = save.error instanceof ApiError ? save.error.problems : []
  const problem = (param: string) => problems.find((p) => p.param === param)?.message
  const general = problems.filter((p) => !p.param)

  const submit = handleSubmit(({ type: typeName, name, values }) => {
    // Only the chosen type's fields: switching types leaves others' values behind.
    const own = Object.fromEntries((type?.fields ?? []).map((f) => [f.name, values[f.name] ?? '']))
    const done = () => {
      toast.success(credential ? 'Credential saved' : `Created ${name.trim()}`)
      onClose()
    }

    if (credential) update.mutate({ id: credential.id, name, values: own }, { onSuccess: done })
    else create.mutate({ type: typeName, name, values: own }, { onSuccess: done })
  })

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{credential ? `Edit ${credential.name}` : 'New credential'}</DialogTitle>
          <DialogDescription>{type?.description}</DialogDescription>
        </DialogHeader>

        <form id="credential-form" className="flex flex-col gap-4" onSubmit={submit}>
          {save.isError && problems.length === 0 && <ErrorAlert error={save.error} />}
          {general.length > 0 && (
            <Alert variant="destructive">
              <CircleAlert />
              <AlertDescription>
                {general.map((p) => (
                  <p key={p.message}>{p.message}</p>
                ))}
              </AlertDescription>
            </Alert>
          )}

          {!credential && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="credential-type">Kind</Label>
              <NativeSelect id="credential-type" className="w-full" {...register('type')}>
                {types.map((t) => (
                  <NativeSelectOption key={t.name} value={t.name}>
                    {t.label}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          )}

          <Field id="credential-name" label="Name" problem={problem('name')}>
            <Input
              id="credential-name"
              autoComplete="off"
              placeholder="e.g. Alerts relay"
              aria-invalid={Boolean(problem('name')) || undefined}
              {...register('name')}
            />
          </Field>

          {type?.fields.map((f) => {
            const id = `credential-${f.name}`
            const saved = credential?.secrets.includes(f.name)
            const hint = [f.description, f.secret && 'Stored encrypted and never shown again.']

            return (
              <Field
                key={`${type.name}-${f.name}`}
                id={id}
                label={f.label}
                hint={hint.filter(Boolean).join(' ')}
                problem={problem(`values.${f.name}`)}
              >
                <Input
                  id={id}
                  type={f.secret ? 'password' : 'text'}
                  autoComplete="off"
                  className="font-mono"
                  placeholder={
                    saved ? 'Saved. Type a new value to replace it.' : f.required ? '' : 'Optional'
                  }
                  aria-invalid={Boolean(problem(`values.${f.name}`)) || undefined}
                  aria-describedby={`${id}-hint`}
                  {...register(`values.${f.name}`)}
                />
              </Field>
            )
          })}
        </form>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="credential-form" disabled={save.isPending}>
            {credential ? 'Save' : 'Create'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Field({
  id,
  label,
  hint,
  problem,
  children,
}: {
  id: string
  label: string
  hint?: string
  problem?: string
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      <div id={`${id}-hint`} className="flex flex-col gap-1 text-xs">
        {hint && <p className="text-muted-foreground">{hint}</p>}
        {problem && (
          <p role="alert" className="text-destructive">
            {problem}
          </p>
        )}
      </div>
    </div>
  )
}
