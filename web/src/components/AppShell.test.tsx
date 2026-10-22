import '@/test/browser'

import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Schemas } from '@/api/client'
import { alice, signedIn } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const root: Schemas['User'] = { ...alice, id: 'u-0', email: 'root@example.com', role: 'admin' }

const noWorkflows = http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] }))

describe('the top bar (D368)', () => {
  it('has the main pages, and Trading in a menu marked while one of its pages is open', async () => {
    server.use(
      signedIn,
      noWorkflows,
      http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [] })),
    )
    const { user, router } = renderApp('/workflows')

    const pages = within(await screen.findByRole('navigation', { name: 'Pages' }))
    for (const name of ['Workflows', 'Library', 'Nodes']) {
      expect(pages.getByRole('link', { name })).toBeInTheDocument()
    }
    expect(pages.queryByRole('link', { name: 'Wallets' })).not.toBeInTheDocument()

    await user.click(pages.getByRole('button', { name: 'Trading' }))
    const menu = within(await screen.findByRole('menu'))
    expect(menu.getByRole('menuitem', { name: 'Live orders' })).toHaveAttribute('href', '/orders')
    await user.click(menu.getByRole('menuitem', { name: 'Wallets' }))

    expect(router.state.location.pathname).toBe('/wallets')
    expect(pages.getByRole('button', { name: 'Trading' })).toHaveClass('text-foreground')
  })

  it('links the docs and the repository, each in a tab of its own', async () => {
    server.use(signedIn, noWorkflows)
    renderApp('/workflows')

    expect(await screen.findByRole('link', { name: 'Docs' })).toHaveAttribute('target', '_blank')
    const github = screen.getByRole('link', { name: 'GitHub' })
    expect(github).toHaveAttribute('href', 'https://github.com/lein3000zzz/polythought-go')
    expect(github).toHaveAttribute('target', '_blank')
    expect(github).not.toHaveTextContent(/\d/) // GitHub has not said
  })

  it("shows the repository's stars while the server knows them (D372)", async () => {
    server.use(
      signedIn,
      noWorkflows,
      http.get('/api/v1/meta', () =>
        HttpResponse.json({ repo: 'https://github.com/lein3000zzz/polythought-go', stars: 1234 }),
      ),
    )
    renderApp('/workflows')

    const github = await screen.findByRole('link', { name: 'GitHub, 1,234 stars' })
    expect(github).toHaveTextContent('1.2k')
    expect(github).toHaveAttribute('title', '1,234 stars on GitHub')
  })

  it('keeps who is signed in, their pages and the theme in the account menu', async () => {
    server.use(signedIn, noWorkflows)
    const { user } = renderApp('/workflows')

    const account = await screen.findByRole('button', { name: 'Account' })
    expect(account).toHaveTextContent('a')
    expect(account).toHaveAttribute('title', 'alice@example.com')

    await user.click(account)
    const menu = within(await screen.findByRole('menu'))
    expect(menu.getByText('alice@example.com')).toBeInTheDocument()
    expect(menu.getByRole('menuitem', { name: 'Settings' })).toHaveAttribute('href', '/settings')
    expect(menu.getByRole('menuitem', { name: 'Credentials' })).toHaveAttribute(
      'href',
      '/credentials',
    )
    expect(menu.queryByRole('menuitem', { name: 'Admin' })).not.toBeInTheDocument()
    expect(menu.getByRole('menuitemradio', { name: 'System' })).toBeChecked()
    expect(menu.getByRole('menuitem', { name: 'Sign out' })).toBeInTheDocument()
  })

  it("shows an admin's role and their Admin page", async () => {
    server.use(
      http.get('/api/v1/me', () => HttpResponse.json(root)),
      noWorkflows,
    )
    const { user } = renderApp('/workflows')

    await user.click(await screen.findByRole('button', { name: 'Account' }))
    const menu = within(await screen.findByRole('menu'))
    expect(menu.getByText('Admin', { selector: '[data-slot=badge]' })).toBeInTheDocument()
    expect(menu.getByRole('menuitem', { name: 'Admin' })).toHaveAttribute('href', '/admin')
  })

  it('folds every page into one menu for narrow screens', async () => {
    server.use(signedIn, noWorkflows)
    const { user } = renderApp('/workflows')

    await user.click(await screen.findByRole('button', { name: 'Pages' }))
    const menu = within(await screen.findByRole('menu'))
    for (const name of ['Workflows', 'Library', 'Nodes', 'Wallets', 'Live orders']) {
      expect(menu.getByRole('menuitem', { name })).toBeInTheDocument()
    }
    expect(menu.getByRole('menuitem', { name: 'Workflows' })).toHaveAttribute(
      'aria-current',
      'page',
    )
  })
})
