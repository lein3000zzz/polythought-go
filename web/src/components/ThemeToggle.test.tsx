import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { registration, signedIn, signedOut } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

const isDark = () => document.documentElement.classList.contains('dark')

describe('theme switcher', () => {
  it('switches the app between light, dark and the system theme', async () => {
    server.use(
      signedIn,
      http.get('/api/v1/workflows', () => HttpResponse.json({ workflows: [] })),
    )
    const { user } = renderApp('/workflows')

    // In the app, the account menu has it (D368).
    await user.click(await screen.findByRole('button', { name: 'Account' }))
    const menu = within(await screen.findByRole('menu'))
    expect(menu.getByRole('menuitemradio', { name: 'System' })).toBeChecked()

    await user.click(menu.getByRole('menuitemradio', { name: 'Dark' }))
    expect(isDark()).toBe(true)
    expect(localStorage.getItem('polythought.theme')).toBe('dark')

    await user.click(screen.getByRole('button', { name: 'Account' }))
    await user.click(await screen.findByRole('menuitemradio', { name: 'Light' }))
    expect(isDark()).toBe(false)
  })

  it('is on the sign-in page too', async () => {
    server.use(signedOut, registration('invite'))
    const { user } = renderApp('/login')

    await user.click(await screen.findByRole('button', { name: 'Theme' }))
    await user.click(await screen.findByRole('menuitemradio', { name: 'Dark' }))
    expect(isDark()).toBe(true)
  })
})
