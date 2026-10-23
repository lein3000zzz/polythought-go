import { useForm } from 'react-hook-form'
import { toast } from 'sonner'

import { useChangePassword } from '@/api/account'
import { useMinPasswordLength } from '@/api/auth'
import { ApiError } from '@/api/client'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import { jumpId } from './pages'

type PasswordForm = { currentPassword: string; newPassword: string; repeat: string }

export function PasswordSection() {
  const minPasswordLength = useMinPasswordLength()
  const change = useChangePassword()
  const { register, handleSubmit, reset, formState, setError } = useForm<PasswordForm>()
  const wrongPassword =
    change.error instanceof ApiError && change.error.code === 'invalid_credentials'

  return (
    <section id={jumpId('password')} className="flex flex-col gap-4">
      <div>
        <h2 className="font-medium">Password</h2>
        <p className="text-sm text-muted-foreground">Changing it signs you out everywhere else.</p>
      </div>

      <form
        className="flex max-w-sm flex-col gap-4"
        onSubmit={handleSubmit(({ currentPassword, newPassword, repeat }) => {
          if (newPassword !== repeat) {
            setError('repeat', { message: 'The new passwords differ' })
            return
          }

          change.mutate(
            { currentPassword, newPassword },
            {
              onSuccess: () => {
                reset()
                toast.success('Password changed')
              },
            },
          )
        })}
      >
        {change.isError && !wrongPassword && <ErrorAlert error={change.error} />}

        <div className="flex flex-col gap-2">
          <Label htmlFor="current-password">Current password</Label>
          <Input
            id="current-password"
            type="password"
            autoComplete="current-password"
            required
            aria-invalid={wrongPassword || undefined}
            {...register('currentPassword')}
          />
          {wrongPassword && (
            <p role="alert" className="text-xs text-destructive">
              That is not your current password.
            </p>
          )}
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="new-password">New password</Label>
          <Input
            id="new-password"
            type="password"
            autoComplete="new-password"
            minLength={minPasswordLength}
            required
            aria-describedby="new-password-hint"
            {...register('newPassword')}
          />
          <p id="new-password-hint" className="text-xs text-muted-foreground">
            {minPasswordLength && `At least ${minPasswordLength} characters.`}
          </p>
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="repeat-password">Repeat the new password</Label>
          <Input
            id="repeat-password"
            type="password"
            autoComplete="new-password"
            required
            aria-invalid={Boolean(formState.errors.repeat) || undefined}
            {...register('repeat')}
          />
          {formState.errors.repeat && (
            <p role="alert" className="text-xs text-destructive">
              {formState.errors.repeat.message}
            </p>
          )}
        </div>

        <Button type="submit" className="self-start" disabled={change.isPending}>
          Change password
        </Button>
      </form>
    </section>
  )
}
