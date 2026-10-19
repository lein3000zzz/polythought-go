import { useForm, useWatch } from 'react-hook-form'
import { Link, Navigate, useSearchParams } from 'react-router'

import { useAuthConfig, useLogin, useMe } from '@/api/auth'
import { ApiError } from '@/api/client'
import { AuthLayout } from '@/components/AuthLayout'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ResendLink } from '@/features/auth/Verification'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { nextPath } from '@/lib/paths'

type Form = { email: string; password: string }

export function LoginPage() {
  const me = useMe()
  const config = useAuthConfig()
  const login = useLogin()
  const [params] = useSearchParams()
  // The verification page sends people here with their address (D172).
  const { register, handleSubmit, control } = useForm<Form>({
    defaultValues: { email: params.get('email') ?? '' },
  })
  const email = useWatch({ control, name: 'email' })
  const unverified = login.error instanceof ApiError && login.error.code === 'email_not_verified'

  // Signing in fills in the user, which lands here and moves on.
  if (me.data) return <Navigate to={nextPath(params.get('next'))} replace />

  return (
    <AuthLayout title="Sign in">
      <form className="flex flex-col gap-4" onSubmit={handleSubmit((form) => login.mutate(form))}>
        {params.get('verified') && !login.isError && (
          <Alert>
            <AlertDescription>Your address is verified: sign in.</AlertDescription>
          </Alert>
        )}
        {params.get('reset') && !login.isError && (
          <Alert>
            <AlertDescription>Your password is changed: sign in with it.</AlertDescription>
          </Alert>
        )}
        {login.isError && <ErrorAlert error={login.error} />}
        {unverified && <ResendLink email={email.trim()} />}

        <div className="flex flex-col gap-2">
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" autoComplete="username" required {...register('email')} />
        </div>

        <div className="flex flex-col gap-2">
          <div className="flex items-baseline justify-between">
            <Label htmlFor="password">Password</Label>
            {config.data?.passwordReset && (
              <Link
                to={`/forgot-password${email ? `?email=${encodeURIComponent(email.trim())}` : ''}`}
                className="text-sm text-muted-foreground underline underline-offset-4"
              >
                Forgot password?
              </Link>
            )}
          </div>
          <Input
            id="password"
            type="password"
            autoComplete="current-password"
            required
            {...register('password')}
          />
        </div>

        <Button type="submit" disabled={login.isPending}>
          Sign in
        </Button>
      </form>

      {config.data && config.data.registration !== 'closed' && (
        <p className="mt-4 text-center text-sm text-muted-foreground">
          No account yet?{' '}
          <Link to="/register" className="text-foreground underline underline-offset-4">
            Create one
          </Link>
        </p>
      )}
    </AuthLayout>
  )
}
