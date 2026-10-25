import '@/test/browser'

import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { alice } from '@/test/fixtures'
import { renderApp } from '@/test/render'
import { server } from '@/test/server'

// serving keeps the keys the page sets, as the server does.
function serving(own: Record<string, string> = {}) {
  const sent: Record<string, string>[] = []
  let shortcuts = own

  server.use(
    http.get('/api/v1/me', () => HttpResponse.json({ ...alice, shortcuts })),
    http.put('/api/v1/me/shortcuts', async ({ request }) => {
      const body = (await request.json()) as { shortcuts: Record<string, string> }
      sent.push(body.shortcuts)
      shortcuts = body.shortcuts

      return new HttpResponse(null, { status: 204 })
    }),
  )

  return sent
}

const row = (label: string) => screen.getByText(label).closest('li') as HTMLElement

describe('the editor shortcuts in Settings (D420)', () => {
  it('changes one by pressing its new keys, and puts it back', async () => {
    const sent = serving()
    const { user } = renderApp('/settings/shortcuts')

    await screen.findByText('Workflow editor shortcuts')
    expect(within(row('Undo')).getByText('Ctrl+Z')).toBeInTheDocument()

    await user.click(within(row('Undo')).getByRole('button', { name: 'Change' }))
    fireEvent.keyDown(window, { key: 'u', code: 'KeyU', ctrlKey: true })

    await waitFor(() => expect(sent).toEqual([{ undo: 'mod+u' }]))
    expect(await within(row('Undo')).findByText('Ctrl+U')).toBeInTheDocument()

    await user.click(within(row('Undo')).getByRole('button', { name: 'Reset' }))
    await waitFor(() => expect(sent.at(-1)).toEqual({}))
  })

  it('refuses keys another has, or that stay as they are, and Esc changes nothing', async () => {
    const sent = serving({ help: 'mod+k' })
    const { user } = renderApp('/settings/shortcuts')
    await screen.findByText('Workflow editor shortcuts')

    await user.click(within(row('Redo')).getByRole('button', { name: 'Change' }))
    fireEvent.keyDown(window, { key: 'v', code: 'KeyV', ctrlKey: true })
    expect(await screen.findByText('Copy, cut and paste keep their keys')).toBeInTheDocument()

    fireEvent.keyDown(window, { key: 'k', code: 'KeyK', ctrlKey: true })
    expect(await screen.findByText(/List the shortcuts.*already/)).toBeInTheDocument()

    await user.click(within(row('Save')).getByRole('button', { name: 'Change' }))
    fireEvent.keyDown(window, { key: 'Escape', code: 'Escape' })
    expect(within(row('Save')).getByText('Ctrl+S')).toBeInTheDocument()

    expect(sent).toEqual([])

    await user.click(screen.getByRole('button', { name: 'Reset all' }))
    await waitFor(() => expect(sent).toEqual([{}]))
  })
})
