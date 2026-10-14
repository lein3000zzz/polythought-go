import { useForm } from 'react-hook-form'
import { Link, Navigate, useSearchParams } from 'react-router'

import { useAuthConfig, useMe, useRegister } from '@/api/auth'
import { AuthLayout } from '@/components/AuthLayout'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

type Form = { email: string; password: string; inviteCode: string }

export function RegisterPage() {
  const me = useMe()
  const config = useAuthConfig()
  const minPasswordLength = config.data?.minPasswordLength
  const registration = useRegister()
  const [params] = useSearchParams()
  const { register, handleSubmit } = useForm<Form>({
    defaultValues: { inviteCode: params.get('invite') ?? '' },
  })

  if (me.data) return <Navigate to="/workflows" replace />

  const signIn = (
    <p className="mt-4 text-center text-sm text-muted-foreground">
      Already have an account?{' '}
      <Link to="/login" className="text-foreground underline underline-offset-4">
        Sign in
      </Link>
    </p>
  )

  if (config.isPending) return <AuthLayout title="Create an account">{null}</AuthLayout>

  if (config.isError) {
    return (
      <AuthLayout title="Create an account">
        <ErrorAlert error={config.error} onRetry={() => void config.refetch()} />
      </AuthLayout>
    )
  }

  const mode = config.data.registration

  if (mode === 'closed') {
    return (
      <AuthLayout
        title="Registration is closed"
        description="Accounts on this server are created by an admin. Ask one for access."
      >
        {signIn}
      </AuthLayout>
    )
  }

  return (
    <AuthLayout
      title="Create an account"
      description={mode === 'invite' ? 'You need an invite code from an admin.' : undefined}
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={handleSubmit(({ email, password, inviteCode }) =>
          registration.mutate({
            email,
            password,
            ...(mode === 'invite' && { inviteCode: inviteCode.trim() }),
          }),
        )}
      >
        {registration.isError && <ErrorAlert error={registration.error} />}

        <div className="flex flex-col gap-2">
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" autoComplete="email" required {...register('email')} />
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="password">Password</Label>
          <Input
            id="password"
            type="password"
            autoComplete="new-password"
            minLength={minPasswordLength}
            required
            aria-describedby="password-hint"
            {...register('password')}
          />
          <p id="password-hint" className="text-xs text-muted-foreground">
            {minPasswordLength && `At least ${minPasswordLength} characters.`}
          </p>
        </div>

        {mode === 'invite' && (
          <div className="flex flex-col gap-2">
            <Label htmlFor="inviteCode">Invite code</Label>
            <Input id="inviteCode" autoComplete="off" required {...register('inviteCode')} />
          </div>
        )}

        <Button type="submit" disabled={registration.isPending}>
          Create account
        </Button>
      </form>

      {signIn}
    </AuthLayout>
  )
}
