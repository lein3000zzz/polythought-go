import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { useAuthConfig, useConfirmJoin, useRequestToJoin } from '@/api/auth'
import { AuthLayout } from '@/components/AuthLayout'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

const noteMost = 500

const signInLink = (
  <p className="mt-4 text-center text-sm text-muted-foreground">
    Have an invite?{' '}
    <Link to="/register" className="text-foreground underline underline-offset-4">
      Create your account
    </Link>
  </p>
)

/**
 * Asking for an invite while requests are open (D394): the address and a note. Opened
 * from the mailed link, it confirms the request, which then goes to the admins.
 */
export function RequestAccessPage() {
  const [params] = useSearchParams()
  const token = params.get('token')

  return token ? <Confirm token={token} /> : <Ask />
}

function Ask() {
  const config = useAuthConfig()
  const ask = useRequestToJoin()
  const [email, setEmail] = useState('')
  const [note, setNote] = useState('')

  if (config.isPending) return <AuthLayout title="Ask for an invite">{null}</AuthLayout>

  if (!config.data?.requests) {
    return (
      <AuthLayout
        title="Ask for an invite"
        description="This instance takes no requests to join: ask an admin you know for an invite."
      >
        {signInLink}
      </AuthLayout>
    )
  }

  if (ask.isSuccess) {
    return (
      <AuthLayout
        title="Check your inbox"
        description={`If ${email} can ask, a link to confirm the request is on its way. Open it within 24 hours: the admins see your request then, and mail you an invite if they approve it.`}
      >
        {signInLink}
      </AuthLayout>
    )
  }

  return (
    <AuthLayout
      title="Ask for an invite"
      description="Say who you are: the admins decide, and mail you an invite if they approve."
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          ask.mutate({ email: email.trim(), note: note.trim() })
        }}
      >
        {ask.isError && <ErrorAlert error={ask.error} />}

        <div className="flex flex-col gap-2">
          <Label htmlFor="email">Email</Label>
          <Input
            id="email"
            type="email"
            autoComplete="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="note">Note (optional)</Label>
          <Textarea
            id="note"
            rows={4}
            maxLength={noteMost}
            aria-describedby="note-hint"
            placeholder="Who you are, and why you would like in."
            value={note}
            onChange={(e) => setNote(e.target.value)}
          />
          <p id="note-hint" className="text-xs text-muted-foreground">
            {note.length} of {noteMost} characters.
          </p>
        </div>

        <Button type="submit" disabled={ask.isPending || !email.trim()}>
          Ask for an invite
        </Button>
      </form>

      {signInLink}
    </AuthLayout>
  )
}

function Confirm({ token }: { token: string }) {
  const confirm = useConfirmJoin()
  // A link works once, so it is sent once, however often the page mounts.
  const sent = useRef(false)

  useEffect(() => {
    if (sent.current) return
    sent.current = true

    confirm.mutate(token)
  }, [token, confirm])

  if (confirm.isError) {
    return (
      <AuthLayout
        title="This link does not work"
        description="It works once, for 24 hours. Ask again for a new one."
      >
        <div className="flex flex-col gap-4">
          <ErrorAlert error={confirm.error} />
          <Button asChild variant="outline">
            <Link to="/request-access">Ask again</Link>
          </Button>
        </div>
      </AuthLayout>
    )
  }

  if (confirm.isSuccess) {
    return (
      <AuthLayout
        title="Your request is with the admins"
        description="They will mail you an invite at this address if they approve it."
      >
        {signInLink}
      </AuthLayout>
    )
  }

  return <AuthLayout title="Confirming your request…">{null}</AuthLayout>
}
