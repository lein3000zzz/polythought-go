import { useEffect, useRef } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'

import { useVerifyEmail } from '@/api/auth'
import { AuthLayout } from '@/components/AuthLayout'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ResendForm } from '@/features/auth/Verification'

/**
 * Opens a mailed verification link: it verifies the address, then goes on to the sign-in
 * form with it filled in; the password is still needed (D172).
 */
export function VerifyEmailPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const verify = useVerifyEmail()
  // A link works once, so it is sent once, however often the page mounts.
  const sent = useRef(false)

  const token = params.get('token') ?? ''

  useEffect(() => {
    if (sent.current) return
    sent.current = true

    verify.mutate(token, {
      onSuccess: ({ email }) =>
        navigate(`/login?verified=1&email=${encodeURIComponent(email)}`, { replace: true }),
    })
  }, [token, verify, navigate])

  if (verify.isError) {
    return (
      <AuthLayout
        title="This link does not work"
        description="Ask for a new one: it works for 24 hours, and once."
      >
        <div className="flex flex-col gap-4">
          <ErrorAlert error={verify.error} />
          <ResendForm />
          <p className="text-center text-sm text-muted-foreground">
            <Link to="/login" className="text-foreground underline underline-offset-4">
              Sign in
            </Link>
          </p>
        </div>
      </AuthLayout>
    )
  }

  return <AuthLayout title="Verifying your address…">{null}</AuthLayout>
}
