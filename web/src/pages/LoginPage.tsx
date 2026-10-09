import { useForm } from 'react-hook-form'
import { Link, Navigate, useSearchParams } from 'react-router'

import { useAuthConfig, useLogin, useMe } from '@/api/auth'
import { AuthLayout } from '@/components/AuthLayout'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { nextPath } from '@/lib/paths'

type Form = { email: string; password: string }

export function LoginPage() {
  const me = useMe()
  const config = useAuthConfig()
  const login = useLogin()
  const [params] = useSearchParams()
  const { register, handleSubmit } = useForm<Form>()

  // Signing in fills in the user, which lands here and moves on.
  if (me.data) return <Navigate to={nextPath(params.get('next'))} replace />

  return (
    <AuthLayout title="Sign in">
      <form className="flex flex-col gap-4" onSubmit={handleSubmit((form) => login.mutate(form))}>
        {login.isError && <ErrorAlert error={login.error} />}

        <div className="flex flex-col gap-2">
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" autoComplete="username" required {...register('email')} />
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="password">Password</Label>
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
