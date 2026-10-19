import { useState } from 'react'

import { useResendVerification } from '@/api/auth'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

/**
 * Sends the verification link to email again. It says the same whether or not the address
 * has an account that needs one, as the server does.
 */
export function ResendLink({ email }: { email: string }) {
  const resend = useResendVerification()

  return (
    <div className="flex flex-col gap-2">
      <Button
        type="button"
        variant="outline"
        disabled={!email || resend.isPending}
        onClick={() => resend.mutate(email)}
      >
        Send the link again
      </Button>
      {resend.isSuccess && (
        <p className="text-sm text-muted-foreground">
          If {email} still needs one, a new link is on its way. Links go at most once a minute.
        </p>
      )}
      {resend.isError && <ErrorAlert error={resend.error} />}
    </div>
  )
}

/** Asks for the address, then sends it a new verification link. */
export function ResendForm() {
  const [email, setEmail] = useState('')

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-2">
        <Label htmlFor="resend-email">Email</Label>
        <Input
          id="resend-email"
          type="email"
          autoComplete="email"
          value={email}
          onChange={(e) => setEmail(e.target.value.trim())}
        />
      </div>
      <ResendLink email={email} />
    </div>
  )
}
