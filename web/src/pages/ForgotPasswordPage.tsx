import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { useRequestPasswordReset } from '@/api/auth'
import { AuthLayout } from '@/components/AuthLayout'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

/**
 * Asks for a link to set a new password (D176). It says the same whether or not the
 * address has an account, as the server does.
 */
export function ForgotPasswordPage() {
  const [params] = useSearchParams()
  const request = useRequestPasswordReset()
  const [email, setEmail] = useState(params.get('email') ?? '')
  const [sentTo, setSentTo] = useState('')

  const signIn = (
    <p className="mt-4 text-center text-sm text-muted-foreground">
      <Link to="/login" className="text-foreground underline underline-offset-4">
        Back to sign in
      </Link>
    </p>
  )

  if (sentTo) {
    return (
      <AuthLayout
        title="Check your inbox"
        description={`If ${sentTo} has an account, a link to set a new password is on its way. It works once, for an hour.`}
      >
        {signIn}
      </AuthLayout>
    )
  }

  return (
    <AuthLayout title="Forgot your password?" description="We mail you a link to set a new one.">
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          const to = email.trim()
          request.mutate(to, { onSuccess: () => setSentTo(to) })
        }}
      >
        {request.isError && <ErrorAlert error={request.error} />}

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

        <Button type="submit" disabled={request.isPending}>
          Send the link
        </Button>
      </form>

      {signIn}
    </AuthLayout>
  )
}
