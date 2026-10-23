import { CircleAlert, ShieldCheck } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { useMe } from '@/api/auth'
import type { Schemas } from '@/api/client'
import { type AuthRequest, useApprove, useConsent, useDeny } from '@/api/oauth'
import { AuthLayout } from '@/components/AuthLayout'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import { scopeInfo } from '@/features/oauth/scopes'
import { settingsPath } from '@/features/settings/pages'
import { leaveFor } from '@/lib/leave'
import { cn } from 'cn'

type Consent = Schemas['OAuthConsent']
type Decision = Schemas['OAuthDecision']
type Scope = Schemas['OAuthOffer']['scope']

/** The request as the agent sent the browser here with it. */
function requestOf(params: URLSearchParams): AuthRequest {
  const get = (k: string) => params.get(k) ?? ''

  return {
    response_type: get('response_type'),
    client_id: get('client_id'),
    redirect_uri: get('redirect_uri'),
    code_challenge: get('code_challenge'),
    code_challenge_method: get('code_challenge_method'),
    scope: params.get('scope') ?? undefined,
    state: params.get('state') ?? undefined,
    resource: params.get('resource') ?? undefined,
  }
}

/**
 * The consent page an agent sends its user to (D384): who the agent is, the scopes it
 * asks for, live trading and admin unticked, then back to the agent with a code. An agent
 * allowed what it asks before is let through at once.
 */
export function OAuthAuthorizePage() {
  const [params] = useSearchParams()
  const request = useMemo(() => requestOf(params), [params])
  const consent = useConsent(request)
  const approve = useApprove()
  const { mutate: allow } = approve
  const [leaving, setLeaving] = useState(false)
  const started = useRef(false)

  // Back to the agent, with a code or with what went wrong; a problem without a redirect
  // is shown instead.
  const decided = useCallback((d: Decision) => {
    const to = d.redirect ?? d.problem?.redirect
    if (to) {
      setLeaving(true)
      leaveFor(to)
    }
  }, [])

  // A request wrong in a way the agent should hear of goes back to it; one allowed before
  // is approved with what was granted. Once: the page must not send two codes.
  useEffect(() => {
    const data = consent.data
    if (!data || started.current) return

    if (data.problem?.redirect) {
      started.current = true
      leaveFor(data.problem.redirect) // the page says it is going back already
    } else if (data.allowed && data.granted) {
      started.current = true
      allow({ request, scopes: data.granted }, { onSuccess: decided })
    }
  }, [consent.data, request, allow, decided])

  if (consent.isPending) {
    return (
      <AuthLayout title="Allow an agent">
        <Skeleton className="h-48" />
      </AuthLayout>
    )
  }

  if (consent.isError) {
    return (
      <AuthLayout title="Allow an agent">
        <ErrorAlert error={consent.error} onRetry={() => void consent.refetch()} />
      </AuthLayout>
    )
  }

  const data = consent.data
  const name = data.client?.name ?? 'the agent'
  const problem = data.problem ?? approve.data?.problem

  if (leaving || problem?.redirect) {
    return (
      <AuthLayout
        title={`Back to ${name}`}
        description="It picks up from here. If nothing happens, you can close this tab."
      >
        {null}
      </AuthLayout>
    )
  }

  if (problem) {
    return (
      <AuthLayout
        title="This sign-in link does not work"
        description="The agent sent you here with a request Polythought cannot take. Start its sign-in again, or tell whoever made it."
      >
        <div className="flex flex-col gap-4 text-sm">
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{problem.description}</AlertTitle>
            <AlertDescription>
              <code className="text-xs">{problem.code}</code>
            </AlertDescription>
          </Alert>
          <Link to="/workflows" className="text-center underline underline-offset-4">
            Go to Polythought
          </Link>
        </div>
      </AuthLayout>
    )
  }

  if (data.allowed) {
    return (
      <AuthLayout title={`Signing ${name} in`} description="You allowed it this already.">
        {approve.isError ? <ErrorAlert error={approve.error} /> : <Skeleton className="h-8" />}
      </AuthLayout>
    )
  }

  return <ConsentForm request={request} consent={data} onDecided={decided} />
}

type FormProps = {
  request: AuthRequest
  consent: Consent
  onDecided: (d: Decision) => void
}

function ConsentForm({ request, consent, onDecided }: FormProps) {
  const me = useMe()
  const approve = useApprove()
  const deny = useDeny()
  const offered = consent.offered ?? []
  const [ticked, setTicked] = useState(
    () => new Set(offered.filter((o) => o.ticked).map((o) => o.scope)),
  )
  const client = consent.client
  const name = client?.name ?? 'An agent'
  const problem = approve.data?.problem ?? deny.data?.problem
  const busy = approve.isPending || deny.isPending

  const tick = (scope: Scope, on: boolean) =>
    setTicked((was) => {
      const next = new Set(was)
      if (on) next.add(scope)
      else next.delete(scope)
      return next
    })

  return (
    <AuthLayout
      title={`Allow ${name}?`}
      description="An agent asks to work with Polythought as you, over MCP."
    >
      <div className="flex flex-col gap-4 text-sm">
        <div className="flex flex-col gap-1 rounded-md border p-3">
          <span className="flex flex-wrap items-center gap-1.5 font-medium">
            {name}
            {client?.verified ? (
              <Badge variant="secondary">
                <ShieldCheck />
                Verified
              </Badge>
            ) : (
              <Badge variant="outline">Unverified</Badge>
            )}
          </span>
          <span className="text-muted-foreground">
            {client?.verified
              ? `It describes itself at ${client.site}, where only its owner can publish.`
              : `It registered itself, so its name is its own word${client?.site ? `, and so is ${client.site}` : ''}. Allow it only if you just started it.`}
          </span>
        </div>

        <fieldset className="flex flex-col gap-2">
          <legend className="mb-2 font-medium">It may</legend>
          {offered.map((o) => {
            const info = scopeInfo[o.scope]

            return (
              <label
                key={o.scope}
                className={cn(
                  'flex items-start gap-2 rounded-md border p-3',
                  info?.risky && 'border-destructive/50 bg-destructive/5',
                )}
              >
                <Checkbox
                  className="mt-0.5"
                  aria-label={info?.title ?? o.scope}
                  checked={ticked.has(o.scope)}
                  onCheckedChange={(on) => tick(o.scope, on === true)}
                />
                <span className="flex flex-col gap-0.5">
                  <span className="font-medium">{info?.title ?? o.scope}</span>
                  <span className="text-muted-foreground">{info?.detail}</span>
                </span>
              </label>
            )
          })}
        </fieldset>

        {problem && (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{problem.description}</AlertTitle>
          </Alert>
        )}
        {approve.isError && <ErrorAlert error={approve.error} />}
        {deny.isError && <ErrorAlert error={deny.error} />}

        <div className="flex gap-2">
          <Button
            variant="outline"
            className="flex-1"
            disabled={busy}
            onClick={() => deny.mutate(request, { onSuccess: onDecided })}
          >
            Deny
          </Button>
          <Button
            className="flex-1"
            disabled={busy || ticked.size === 0}
            onClick={() =>
              approve.mutate(
                {
                  request,
                  scopes: offered.filter((o) => ticked.has(o.scope)).map((o) => o.scope),
                },
                { onSuccess: onDecided },
              )
            }
          >
            Allow
          </Button>
        </div>

        <p className="text-muted-foreground">
          Then you go back to {consent.redirectHost}. Signed in as{' '}
          <span className="text-foreground">{me.data?.email}</span>; revoke it any time in{' '}
          <Link
            to={settingsPath('connected')}
            className="text-foreground underline underline-offset-4"
          >
            Settings
          </Link>
          .
        </p>
      </div>
    </AuthLayout>
  )
}
