import { Copy, KeyRound, ReceiptText, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { ApiError, errorMessage, type Schemas } from '@/api/client'
import {
  useCredentials,
  useImportKey,
  useLinkWallet,
  useRemoveCredentials,
  useSessionKeys,
  useSetCredentials,
  useTakesKeys,
  useTakesSessionKeys,
  useSetHeartbeat,
  useUnlinkWallet,
  useWallets,
} from '@/api/wallets'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
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
import { Switch } from '@/components/ui/switch'
import { WalletApprovals } from '@/features/wallets/Approvals'
import {
  AuthoriseSessionKeyDialog,
  RemoveSessionKeyDialog,
  SessionKeyStatus,
} from '@/features/wallets/SessionKey'
import { formatDateTime, shortAddress } from '@/lib/format'

type Wallet = Schemas['Wallet']
type SignatureType = NonNullable<Schemas['SetCredentialsRequest']['signatureType']>

const signatureTypes: Record<number, string> = {
  0: 'EOA: a wallet that signs for itself',
  1: 'Polymarket proxy: email or Magic sign-in',
  2: 'Gnosis Safe: browser wallet sign-in',
  3: 'Deposit wallet',
}

export function WalletsPage() {
  const wallets = useWallets()

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
      <div className="flex flex-col gap-1">
        <h1 className="font-heading text-xl font-semibold">Wallets</h1>
        <p className="text-sm text-muted-foreground">
          A linked wallet is enough for nodes that read its positions. Orders and balance also need
          its Polymarket API credentials, which are checked with Polymarket and stored encrypted. To
          trade, a wallet needs a key: a session key, for Deposit Wallets once an admin has entered
          a Builder key, or its private key, where an admin allows importing one.
        </p>
      </div>

      <LinkWalletForm />

      {wallets.isPending && <Skeleton className="h-32" />}
      {wallets.isError && (
        <ErrorAlert error={wallets.error} onRetry={() => void wallets.refetch()} />
      )}
      {wallets.data?.length === 0 && (
        <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">
          No wallets linked yet.
        </p>
      )}

      {wallets.data?.map((w) => (
        <WalletCard key={w.id} wallet={w} />
      ))}
    </div>
  )
}

function LinkWalletForm() {
  const link = useLinkWallet()
  const { register, handleSubmit, reset } = useForm<Schemas['LinkWalletRequest']>()

  return (
    <form
      className="flex flex-col gap-3 rounded-lg border p-4"
      onSubmit={handleSubmit((body) =>
        link.mutate(body, {
          onSuccess: (w) => {
            reset()
            toast.success(`Linked ${w.label || shortAddress(w.address)}`)
          },
        }),
      )}
    >
      <h2 className="text-sm font-medium">Link a wallet</h2>
      {link.isError && <ErrorAlert error={link.error} />}

      <div className="flex flex-wrap items-end gap-3">
        <div className="flex min-w-72 flex-1 flex-col gap-2">
          <Label htmlFor="wallet-address">Address</Label>
          <Input
            id="wallet-address"
            placeholder="0x…"
            className="font-mono"
            autoComplete="off"
            required
            {...register('address')}
          />
        </div>
        <div className="flex w-48 flex-col gap-2">
          <Label htmlFor="wallet-label">Label</Label>
          <Input id="wallet-label" placeholder="Optional" {...register('label')} />
        </div>
        <Button type="submit" disabled={link.isPending}>
          Link
        </Button>
      </div>
    </form>
  )
}

function WalletCard({ wallet }: { wallet: Wallet }) {
  const credentials = useCredentials(wallet.id, wallet.hasCredentials)
  const unlink = useUnlinkWallet()
  const remove = useRemoveCredentials(wallet.id)
  const takesKeys = useTakesKeys()
  const takesSessionKeys = useTakesSessionKeys()
  const sessionKeys = useSessionKeys(wallet.id, Boolean(takesSessionKeys) || wallet.hasSessionKey)
  const activeKey = sessionKeys.data?.active
  const [editing, setEditing] = useState(false)
  const [importing, setImporting] = useState(false)
  const [authorising, setAuthorising] = useState(false)
  const [removingKey, setRemovingKey] = useState(false)
  const [confirmUnlink, setConfirmUnlink] = useState(false)
  const [tradingLive, setTradingLive] = useState<string>()
  const [confirmRemove, setConfirmRemove] = useState(false)
  const heartbeat = useSetHeartbeat(wallet.id)

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-2">
        <div className="flex min-w-0 flex-col gap-1">
          <CardTitle>{wallet.label || 'Wallet'}</CardTitle>
          <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
            <span className="truncate">{wallet.address}</span>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Copy address"
              onClick={() =>
                void navigator.clipboard
                  .writeText(wallet.address)
                  .then(() => toast.success('Address copied'))
              }
            >
              <Copy />
            </Button>
          </span>
        </div>
        <div className="flex shrink-0 gap-1">
          <Button variant="ghost" size="sm" asChild>
            <Link to={`/orders?wallet=${wallet.id}`}>
              <ReceiptText />
              Live orders
            </Link>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive"
            onClick={() => setConfirmUnlink(true)}
          >
            <Trash2 />
            Unlink
          </Button>
        </div>
      </CardHeader>

      <CardContent className="flex flex-col gap-3 text-sm">
        {credentials.isError && <ErrorAlert error={credentials.error} />}
        {sessionKeys.isError && <ErrorAlert error={sessionKeys.error} />}

        {wallet.hasCredentials && credentials.data ? (
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            {wallet.hasKey && <Badge>Can trade</Badge>}
            <Badge variant="secondary">
              <KeyRound />
              {wallet.hasSessionKey ? 'Session key and ' : wallet.hasKey ? 'Private key and ' : ''}
              API key {credentials.data.apiKeyHint}
            </Badge>
            <span className="text-muted-foreground">
              {signatureTypes[credentials.data.signatureType]} · signer{' '}
              <span className="font-mono">{shortAddress(credentials.data.signer)}</span> · updated{' '}
              {formatDateTime(credentials.data.updatedAt)}
            </span>
          </div>
        ) : (
          !wallet.hasCredentials && <p className="text-muted-foreground">No API credentials.</p>
        )}

        {sessionKeys.data && <SessionKeyStatus keys={sessionKeys.data} />}
        <WalletApprovals wallet={wallet} />

        {(wallet.hasKey || wallet.hasSessionKey) && (
          <div className="flex items-start gap-2">
            <Switch
              id={`heartbeat-${wallet.id}`}
              checked={wallet.heartbeat}
              disabled={heartbeat.isPending}
              onCheckedChange={(on) =>
                heartbeat.mutate(on, {
                  onSuccess: () =>
                    toast.success(on ? 'Heartbeats on' : 'Heartbeats off once nothing rests'),
                  onError: (error) => toast.error(errorMessage(error)),
                })
              }
            />
            <div className="flex flex-col gap-0.5">
              <Label htmlFor={`heartbeat-${wallet.id}`}>
                Cancel its resting orders if the server goes quiet
              </Label>
              <span className="text-xs text-muted-foreground">
                While live orders rest, the server tells Polymarket every 5 seconds that it is
                there; missed for 10 seconds, by a restart or a network blip too, Polymarket cancels
                every order of the wallet resting on the book. Turned off, the heartbeats stop once
                no orders rest.
              </span>
            </div>
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          {takesSessionKeys && (
            <Button
              variant="outline"
              size="sm"
              disabled={sessionKeys.data?.pending?.state === 'authorizing'}
              onClick={() => setAuthorising(true)}
            >
              {activeKey ? 'Renew session key' : 'Authorise a session key'}
            </Button>
          )}
          {activeKey && (
            <Button variant="ghost" size="sm" onClick={() => setRemovingKey(true)}>
              Remove session key
            </Button>
          )}
          {/* A session key's credentials go with it (D159). */}
          {!wallet.hasSessionKey && (
            <>
              {takesKeys && (
                <Button variant="outline" size="sm" onClick={() => setImporting(true)}>
                  {wallet.hasKey ? 'Replace private key' : 'Import private key'}
                </Button>
              )}
              <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
                {wallet.hasCredentials ? 'Replace credentials' : 'Add credentials'}
              </Button>
              {wallet.hasCredentials && (
                <Button variant="ghost" size="sm" onClick={() => setConfirmRemove(true)}>
                  Remove credentials
                </Button>
              )}
            </>
          )}
        </div>
      </CardContent>

      <CredentialsDialog wallet={wallet} open={editing} onClose={() => setEditing(false)} />
      <ImportKeyDialog wallet={wallet} open={importing} onClose={() => setImporting(false)} />
      <AuthoriseSessionKeyDialog
        wallet={wallet}
        renewing={Boolean(activeKey)}
        open={authorising}
        onClose={() => setAuthorising(false)}
      />
      {activeKey && (
        <RemoveSessionKeyDialog
          wallet={wallet}
          sessionKey={activeKey}
          open={removingKey}
          onClose={() => setRemovingKey(false)}
        />
      )}

      <ConfirmDialog
        open={confirmUnlink}
        title={`Unlink ${wallet.label || shortAddress(wallet.address)}?`}
        description={
          wallet.hasSessionKey
            ? 'Its session key and credentials are deleted too, without revoking the key: remove the session key first to revoke it. Workflow nodes that use this wallet will fail until you choose another.'
            : 'Its stored credentials are deleted too. Workflow nodes that use this wallet will fail until you choose another.'
        }
        confirm="Unlink"
        destructive
        onCancel={() => setConfirmUnlink(false)}
        onConfirm={() =>
          unlink.mutate(
            { id: wallet.id },
            {
              onSuccess: () => toast.success('Wallet unlinked'),
              onError: (error) =>
                // Workflows trade live from it: they are named, and asked about (D218).
                error instanceof ApiError && error.code === 'wallet_trades_live'
                  ? setTradingLive(errorMessage(error))
                  : toast.error(errorMessage(error)),
            },
          )
        }
      />

      <ConfirmDialog
        open={tradingLive !== undefined}
        title={`Unlink ${wallet.label || shortAddress(wallet.address)} from live trading?`}
        description={`${tradingLive ?? ''}.`}
        confirm="Unlink, back to paper"
        destructive
        onCancel={() => setTradingLive(undefined)}
        onConfirm={() =>
          unlink.mutate(
            { id: wallet.id, confirm: true },
            {
              onSuccess: () => toast.success('Wallet unlinked; its workflows trade on paper'),
              onError: (error) => toast.error(errorMessage(error)),
            },
          )
        }
      />

      <ConfirmDialog
        open={confirmRemove}
        title={
          wallet.hasKey
            ? 'Remove the private key and API credentials?'
            : 'Remove the API credentials?'
        }
        description={
          wallet.hasKey
            ? 'Both are deleted, and this wallet can no longer trade. Nodes that read its orders or balance will fail until you add them again.'
            : "Nodes that read this wallet's orders or balance will fail until you add them again."
        }
        confirm="Remove"
        destructive
        onCancel={() => setConfirmRemove(false)}
        onConfirm={() =>
          remove.mutate(undefined, {
            onSuccess: () => toast.success('Credentials removed'),
            onError: (error) => toast.error(errorMessage(error)),
          })
        }
      />
    </Card>
  )
}

type CredentialsForm = {
  apiKey: string
  secret: string
  passphrase: string
  signer: string
  signatureType: string
}

function CredentialsDialog({
  wallet,
  open,
  onClose,
}: {
  wallet: Wallet
  open: boolean
  onClose: () => void
}) {
  const save = useSetCredentials(wallet.id)
  const { register, handleSubmit, reset } = useForm<CredentialsForm>({
    defaultValues: { signatureType: '0' },
  })

  const close = () => {
    reset() // the secrets do not linger in the form
    save.reset()
    onClose()
  }

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && close()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>API credentials</DialogTitle>
          <DialogDescription>
            The CLOB API key of {wallet.label || shortAddress(wallet.address)}. Polymarket checks
            them before they are stored; they are never shown again.
          </DialogDescription>
        </DialogHeader>

        <form
          id="credentials-form"
          className="flex flex-col gap-4"
          onSubmit={handleSubmit(({ signer, signatureType, ...secrets }) =>
            save.mutate(
              {
                ...secrets,
                signatureType: Number(signatureType) as SignatureType,
                ...(signer.trim() ? { signer: signer.trim() } : {}),
              },
              {
                onSuccess: () => {
                  toast.success('Credentials checked and saved')
                  close()
                },
              },
            ),
          )}
        >
          {save.isError && <ErrorAlert error={save.error} />}

          {(['apiKey', 'secret', 'passphrase'] as const).map((field) => (
            <div key={field} className="flex flex-col gap-2">
              <Label htmlFor={`cred-${field}`}>
                {{ apiKey: 'API key', secret: 'Secret', passphrase: 'Passphrase' }[field]}
              </Label>
              <Input
                id={`cred-${field}`}
                type={field === 'apiKey' ? 'text' : 'password'}
                autoComplete="off"
                className="font-mono"
                required
                {...register(field)}
              />
            </div>
          ))}

          <div className="flex flex-col gap-2">
            <Label htmlFor="cred-type">Account type</Label>
            <NativeSelect id="cred-type" className="w-full" {...register('signatureType')}>
              {Object.entries(signatureTypes).map(([value, label]) => (
                <NativeSelectOption key={value} value={value}>
                  {label}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="cred-signer">Signer</Label>
            <Input
              id="cred-signer"
              placeholder={wallet.address}
              className="font-mono"
              autoComplete="off"
              aria-describedby="cred-signer-hint"
              {...register('signer')}
            />
            <p id="cred-signer-hint" className="text-xs text-muted-foreground">
              Leave empty for EOA and deposit wallets. Proxy and Safe accounts: the address that
              owns the API key.
            </p>
          </div>
        </form>

        <DialogFooter>
          <Button variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button type="submit" form="credentials-form" disabled={save.isPending}>
            {save.isPending ? 'Checking…' : 'Check and save'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Takes a wallet's private key (D153): checked to control the wallet (D155), kept
 * encrypted, and used to make its API credentials (D154). It is never shown again.
 */
function ImportKeyDialog({
  wallet,
  open,
  onClose,
}: {
  wallet: Wallet
  open: boolean
  onClose: () => void
}) {
  const importKey = useImportKey(wallet.id)
  const [key, setKey] = useState('')

  const close = () => {
    setKey('')
    importKey.reset()
    onClose()
  }

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && close()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Import the private key of {wallet.label || shortAddress(wallet.address)}
          </DialogTitle>
          <DialogDescription>
            With it, Polythought can place this wallet&apos;s orders. It must be the key of the
            wallet itself, or of the account that owns its proxy, Safe or Deposit Wallet:
            Polythought checks that before keeping it. It is kept encrypted, makes the wallet&apos;s
            API credentials, replacing any added before, and is never shown again. Use a wallet that
            holds only what you mean to trade.
          </DialogDescription>
        </DialogHeader>

        <form
          id={`import-key-${wallet.id}`}
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            importKey.mutate(key, {
              onSuccess: () => {
                toast.success('Key imported: this wallet can trade')
                close()
              },
            })
          }}
        >
          {importKey.isError && <ErrorAlert error={importKey.error} />}
          <Label htmlFor={`private-key-${wallet.id}`}>Private key</Label>
          <Input
            id={`private-key-${wallet.id}`}
            type="password"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            placeholder="0x… (64 hex digits)"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            required
          />
        </form>

        <DialogFooter>
          <Button variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button
            type="submit"
            form={`import-key-${wallet.id}`}
            disabled={importKey.isPending || !key}
          >
            Import
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
