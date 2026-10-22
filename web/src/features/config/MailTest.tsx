import { useState } from 'react'

import { useMe } from '@/api/auth'
import { useSendTestMail, type Setting } from '@/api/settings'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

/**
 * Sends a test email through the mail server as saved, not as edited (D168), and says what
 * the server answered when it did not take it.
 */
export function MailTest({ settings }: { settings: Map<string, Setting> }) {
  const me = useMe()
  const send = useSendTestMail()
  const [to, setTo] = useState<string | null>(null)
  const [sentTo, setSentTo] = useState('')

  const address = to ?? me.data?.email ?? ''
  const setUp =
    Boolean(settings.get('mail.host')?.value) && Boolean(settings.get('mail.from_address')?.value)

  return (
    <form
      className="flex flex-col gap-2 border-t pt-4"
      onSubmit={(e) => {
        e.preventDefault()
        send.mutate(address, { onSuccess: () => setSentTo(address) })
      }}
    >
      <Label htmlFor="mail-test-to">Send a test email to</Label>
      <div className="flex flex-wrap gap-2">
        <Input
          id="mail-test-to"
          type="email"
          className="max-w-sm"
          value={address}
          onChange={(e) => setTo(e.target.value)}
          required
        />
        <Button type="submit" variant="outline" disabled={!setUp || send.isPending || !address}>
          {send.isPending ? 'Sending…' : 'Send test email'}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        {setUp
          ? 'Uses the saved settings: save changes first.'
          : 'Save a mail server and a From address first.'}
      </p>
      {send.isSuccess && sentTo && (
        <p className="text-sm">Sent to {sentTo}: look in its inbox, and its spam folder.</p>
      )}
      {send.isError && <ErrorAlert error={send.error} />}
    </form>
  )
}
