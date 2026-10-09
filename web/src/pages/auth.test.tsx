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

    await user.click(await screen.findByRole('button', { name: 'Sign out' }))

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
      http.post('/api/v1/auth/register', async ({ request }) => {
        calls.push(`register ${JSON.stringify(await request.json())}`)
        return HttpResponse.json(alice, { status: 201 })
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
