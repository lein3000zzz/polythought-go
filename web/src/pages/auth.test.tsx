import { screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { nextPath } from '@/lib/paths'
import { alice, apiError, registration, signedIn, signedOut, workflow } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const noWorkflows = http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] }))

describe('signing in', () => {
  it('sends signed-out visitors to sign in, remembering where they were going', async () => {
    server.use(signedOut, registration('invite'))

    const { router } = renderApp('/workflows/wf-1')

    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(router.state.location.search).toBe('?next=%2Fworkflows%2Fwf-1')
  })

  it('shows why signing in failed', async () => {
    server.use(
      signedOut,
      registration('invite'),
      http.post('/api/v1/auth/login', () =>
        apiError(401, 'invalid_credentials', 'invalid email or password'),
      ),
    )

    const { user } = renderApp('/login')

    await user.type(await screen.findByLabelText('Email'), 'alice@example.com')
    await user.type(screen.getByLabelText('Password'), 'wrong password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByText('Invalid email or password')).toBeInTheDocument()
  })

  it('goes on to the page that was asked for', async () => {
    let body: unknown

    server.use(
      signedOut,
      registration('invite'),
      noWorkflows,
      http.post('/api/v1/auth/login', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(alice)
      }),
    )

    const { router, user } = renderApp('/login?next=%2Fworkflows')

    await user.type(await screen.findByLabelText('Email'), 'alice@example.com')
    await user.type(screen.getByLabelText('Password'), 'correct horse battery')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByText('No workflows yet.')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/workflows')
    expect(screen.getByText(alice.email)).toBeInTheDocument()
    expect(body).toEqual({ email: 'alice@example.com', password: 'correct horse battery' })
  })

  it('hides the sign-up link when registration is closed', async () => {
    server.use(signedOut, registration('closed'))

    renderApp('/login')

    await screen.findByRole('button', { name: 'Sign in' })
    await waitFor(() => expect(screen.queryByText('Create one')).not.toBeInTheDocument())
  })

  it.each([
    { next: '/workflows/wf-1?x=1', want: '/workflows/wf-1?x=1' },
    { next: null, want: '/workflows' },
    { next: 'https://evil.example', want: '/workflows' },
    { next: '//evil.example', want: '/workflows' },
    { next: '/\\evil.example', want: '/workflows' },
  ])('only follows next paths on this site: $next', ({ next, want }) => {
    expect(nextPath(next)).toBe(want)
  })
})

describe('session', () => {
  it('signs out', async () => {
    let meCalls = 0

    server.use(
      http.get('/api/v1/me', () => {
        meCalls++
        return HttpResponse.json(alice)
      }),
      noWorkflows,
      registration('invite'),
      http.post('/api/v1/auth/logout', () => new HttpResponse(null, { status: 204 })),
    )

    const { router, user } = renderApp('/workflows')

    // The docs are beside the app, outside its router, and open in a tab of their own.
    const docs = await screen.findByRole('link', { name: 'Docs' })
    expect(docs).toHaveAttribute('href', '/docs/')
    expect(docs).toHaveAttribute('target', '_blank')

    await user.click(screen.getByRole('button', { name: 'Sign out' }))

    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(meCalls).toBe(1) // signing out does not ask the server who is signed in
  })

  it('returns to sign-in when the session expires mid-use', async () => {
    server.use(
      signedIn,
      registration('invite'),
      http.get('/api/v1/workflows', () => apiError(401, 'unauthenticated', 'not authenticated')),
    )

    const { router } = renderApp('/workflows')

    expect(await screen.findByRole('button', { name: 'Sign in' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
  })
})

describe('registering', () => {
  it('explains that registration is closed', async () => {
    server.use(signedOut, registration('closed'))

    renderApp('/register')

    expect(await screen.findByText('Registration is closed')).toBeInTheDocument()
    expect(screen.queryByLabelText('Email')).not.toBeInTheDocument()
  })

  it('takes the invite code from the link and signs straight in', async () => {
    const calls: string[] = []

    server.use(
      signedOut,
      registration('invite'),
      noWorkflows,
      http.post('/api/v1/auth/invite', () => HttpResponse.json({})),
      http.post('/api/v1/auth/register', async ({ request }) => {
        calls.push(`register ${JSON.stringify(await request.json())}`)
        return HttpResponse.json({ user: alice, mustVerify: false, sent: false }, { status: 201 })
      }),
      http.post('/api/v1/auth/login', () => {
        calls.push('login')
        return HttpResponse.json(alice)
      }),
    )

    const { router, user } = renderApp('/register?invite=inv_abc')

    expect(await screen.findByLabelText('Invite code')).toHaveValue('inv_abc')

    await user.type(screen.getByLabelText('Email'), 'alice@example.com')
    await user.type(screen.getByLabelText('Password'), 'a long enough password')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/workflows'))
    expect(calls).toEqual([
      'register {"email":"alice@example.com","password":"a long enough password","inviteCode":"inv_abc"}',
      'login',
    ])
  })

  it('shows what the server rejected', async () => {
    server.use(
      signedOut,
      registration('open'),
      http.post('/api/v1/auth/register', () =>
        apiError(409, 'email_taken', 'email already registered'),
      ),
    )

    const { user } = renderApp('/register')

    await user.type(await screen.findByLabelText('Email'), 'alice@example.com')
    await user.type(screen.getByLabelText('Password'), 'a long enough password')
    expect(screen.queryByLabelText('Invite code')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByText('Email already registered')).toBeInTheDocument()
  })
})

describe('a mailed invite', () => {
  it('fills in the address it is for, and keeps it (D181)', async () => {
    let looked: unknown
    let sent: unknown

    server.use(
      signedOut,
      registration('invite'),
      noWorkflows,
      http.post('/api/v1/auth/invite', async ({ request }) => {
        looked = await request.json()
        return HttpResponse.json({ email: 'new@example.com' })
      }),
      http.post('/api/v1/auth/register', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({ user: alice, mustVerify: false, sent: false }, { status: 201 })
      }),
      http.post('/api/v1/auth/login', () => HttpResponse.json(alice)),
    )
    const { user, router } = renderApp('/register?invite=inv_mailed')

    const email = await screen.findByLabelText('Email')
    await waitFor(() => expect(email).toHaveValue('new@example.com'))
    expect(email).toHaveAttribute('readonly')
    expect(screen.getByText('The invite is for this address.')).toBeInTheDocument()
    expect(looked).toEqual({ code: 'inv_mailed' })

    await user.type(screen.getByLabelText('Password'), 'a long enough password')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/workflows'))
    expect(sent).toMatchObject({ email: 'new@example.com', inviteCode: 'inv_mailed' })
  })

  it('says when an invite no longer works', async () => {
    server.use(
      signedOut,
      registration('invite'),
      http.post('/api/v1/auth/invite', () =>
        apiError(400, 'invalid_invite', 'invalid or expired invite'),
      ),
    )
    renderApp('/register?invite=old')

    expect(await screen.findByText('This invite does not work')).toBeInTheDocument()
    expect(screen.getByText('Invalid or expired invite')).toBeInTheDocument()
  })
})

describe('verifying an address', () => {
  const pending = { ...alice, emailVerified: false }

  it('waits for the mailed link instead of signing in, and sends it again', async () => {
    const calls: string[] = []

    server.use(
      signedOut,
      registration('open'),
      http.post('/api/v1/auth/register', () =>
        HttpResponse.json({ user: pending, mustVerify: true, sent: true }, { status: 201 }),
      ),
      http.post('/api/v1/auth/login', () => {
        calls.push('login')
        return HttpResponse.json(alice)
      }),
      http.post('/api/v1/auth/verification', async ({ request }) => {
        calls.push(`again ${JSON.stringify(await request.json())}`)
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/register')

    await user.type(await screen.findByLabelText('Email'), 'alice@example.com')
    await user.type(screen.getByLabelText('Password'), 'a long enough password')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByText('Check your inbox')).toBeInTheDocument()
    expect(screen.getByText(/We mailed a link to alice@example.com/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Send the link again' }))
    expect(await screen.findByText(/a new link is on its way/)).toBeInTheDocument()
    expect(calls).toEqual(['again {"email":"alice@example.com"}'])
  })

  it('says when the mail did not go out (D175)', async () => {
    server.use(
      signedOut,
      registration('open'),
      http.post('/api/v1/auth/register', () =>
        HttpResponse.json({ user: pending, mustVerify: true, sent: false }, { status: 201 }),
      ),
    )
    const { user } = renderApp('/register')

    await user.type(await screen.findByLabelText('Email'), 'alice@example.com')
    await user.type(screen.getByLabelText('Password'), 'a long enough password')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByText(/did not go out/)).toBeInTheDocument()
  })

  it('signing in first says to verify, and sends the link again', async () => {
    let sentTo: unknown

    server.use(
      signedOut,
      registration('invite'),
      http.post('/api/v1/auth/login', () =>
        apiError(403, 'email_not_verified', 'verify your email first: open the link we mailed you'),
      ),
      http.post('/api/v1/auth/verification', async ({ request }) => {
        sentTo = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/login')

    await user.type(await screen.findByLabelText('Email'), 'alice@example.com')
    await user.type(screen.getByLabelText('Password'), 'a long enough password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(
      await screen.findByText('Verify your email first: open the link we mailed you'),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Send the link again' }))
    await waitFor(() => expect(sentTo).toEqual({ email: 'alice@example.com' }))
  })

  it('the link verifies, then signing in asks for the password (D172)', async () => {
    let token: unknown

    server.use(
      signedOut,
      registration('invite'),
      http.post('/api/v1/auth/verify-email', async ({ request }) => {
        token = await request.json()
        return HttpResponse.json({ email: 'alice@example.com' })
      }),
    )
    const { router } = renderApp('/verify-email?token=tok_1')

    expect(await screen.findByText('Your address is verified: sign in.')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(screen.getByLabelText('Email')).toHaveValue('alice@example.com')
    expect(screen.getByLabelText('Password')).toHaveValue('')
    expect(token).toEqual({ token: 'tok_1' })
  })

  it('an old link offers a new one', async () => {
    server.use(
      signedOut,
      registration('invite'),
      http.post('/api/v1/auth/verify-email', () =>
        apiError(
          400,
          'invalid_link',
          'this link does not work any more: it was used already, or it is too old',
        ),
      ),
    )
    renderApp('/verify-email?token=old')

    expect(await screen.findByText('This link does not work')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Send the link again' })).toBeDisabled()
  })
})

describe('resetting a password', () => {
  const resetOn = http.get('/api/v1/auth/config', () =>
    HttpResponse.json({ registration: 'invite', minPasswordLength: 12, passwordReset: true }),
  )

  it('is offered on the sign-in page only while it works', async () => {
    server.use(signedOut, registration('invite'))
    renderApp('/login')

    expect(await screen.findByLabelText('Password')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Forgot password?' })).not.toBeInTheDocument()
  })

  it('mails a link, saying the same for any address', async () => {
    let asked: unknown

    server.use(
      signedOut,
      resetOn,
      http.post('/api/v1/auth/password-reset', async ({ request }) => {
        asked = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = renderApp('/login')

    await user.type(await screen.findByLabelText('Email'), 'alice@example.com')
    await user.click(screen.getByRole('link', { name: 'Forgot password?' }))

    expect(await screen.findByLabelText('Email')).toHaveValue('alice@example.com')
    await user.click(screen.getByRole('button', { name: 'Send the link' }))

    expect(
      await screen.findByText(/If alice@example.com has an account, a link/),
    ).toBeInTheDocument()
    expect(asked).toEqual({ email: 'alice@example.com' })
  })

  it('says when it is off', async () => {
    server.use(
      signedOut,
      resetOn,
      http.post('/api/v1/auth/password-reset', () =>
        apiError(
          403,
          'password_reset_off',
          'passwords cannot be reset by email here: ask an admin',
        ),
      ),
    )
    const { user } = renderApp('/forgot-password')

    await user.type(await screen.findByLabelText('Email'), 'alice@example.com')
    await user.click(screen.getByRole('button', { name: 'Send the link' }))

    expect(
      await screen.findByText('Passwords cannot be reset by email here: ask an admin'),
    ).toBeInTheDocument()
  })

  it('sets the new password, then asks to sign in with it (D177)', async () => {
    let sent: unknown

    server.use(
      signedOut,
      resetOn,
      http.post('/api/v1/auth/password-reset/confirm', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({ email: 'alice@example.com' })
      }),
    )
    const { user, router } = renderApp('/reset-password?token=tok_1')

    await user.type(await screen.findByLabelText('New password'), 'a long enough password')
    await user.click(screen.getByRole('button', { name: 'Set the password' }))

    expect(
      await screen.findByText('Your password is changed: sign in with it.'),
    ).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(screen.getByLabelText('Email')).toHaveValue('alice@example.com')
    expect(sent).toEqual({ token: 'tok_1', password: 'a long enough password' })
  })

  it('an old link offers a new one', async () => {
    server.use(
      signedOut,
      resetOn,
      http.post('/api/v1/auth/password-reset/confirm', () =>
        apiError(
          400,
          'invalid_link',
          'this link does not work any more: it was used already, or it is too old',
        ),
      ),
    )
    const { user } = renderApp('/reset-password?token=old')

    await user.type(await screen.findByLabelText('New password'), 'a long enough password')
    await user.click(screen.getByRole('button', { name: 'Set the password' }))

    expect(await screen.findByRole('link', { name: 'Ask for a new link' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Set the password' })).toBeDisabled()
  })
})

describe('workflow page', () => {
  it('says so when the workflow is not there', async () => {
    server.use(
      signedIn,
      http.get('/api/v1/workflows/:id', () => apiError(404, 'not_found', 'not found')),
      http.get('/api/v1/node-types', () => HttpResponse.json({ nodeTypes: [] })),
    )

    renderApp('/workflows/ghost')

    expect(await screen.findByText(/does not exist/)).toBeInTheDocument()
  })

  it('links from the list to the workflow', async () => {
    server.use(
      signedIn,
      http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [workflow()] })),
    )

    renderApp('/')

    expect(await screen.findByRole('link', { name: 'Spread watch' })).toHaveAttribute(
      'href',
      '/workflows/wf-1',
    )
  })
})
