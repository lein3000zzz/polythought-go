import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'

import { useMinPasswordLength, useResetPassword } from '@/api/auth'
import { ApiError } from '@/api/client'
import { AuthLayout } from '@/components/AuthLayout'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

/**
 * Sets a new password with a mailed link, then goes on to sign in with it (D177). The
 * account is signed out everywhere (D179).
 */
export function ResetPasswordPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const reset = useResetPassword()
  const minPasswordLength = useMinPasswordLength()
  const [password, setPassword] = useState('')

  const token = params.get('token') ?? ''
  const deadLink = reset.error instanceof ApiError && reset.error.code === 'invalid_link'

  return (
    <AuthLayout
      title="Set a new password"
      description="It signs your account out everywhere, and you sign in with it."
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          reset.mutate(
            { token, password },
            {
              onSuccess: ({ email }) =>
                navigate(`/login?reset=1&email=${encodeURIComponent(email)}`, { replace: true }),
            },
          )
        }}
      >
        {reset.isError && <ErrorAlert error={reset.error} />}
        {deadLink && (
          <p className="text-sm">
            <Link to="/forgot-password" className="underline underline-offset-4">
              Ask for a new link
            </Link>
          </p>
        )}

        <div className="flex flex-col gap-2">
          <Label htmlFor="password">New password</Label>
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            minLength={minPasswordLength}
            required
            aria-describedby="password-hint"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <p id="password-hint" className="text-xs text-muted-foreground">
            {minPasswordLength && `At least ${minPasswordLength} characters.`}
          </p>
        </div>

        <Button type="submit" disabled={reset.isPending || deadLink}>
          Set the password
        </Button>
      </form>
    </AuthLayout>
  )
}
