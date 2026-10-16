import '@testing-library/jest-dom/vitest'
// Before the theme module, which looks for matchMedia as it loads.
import './media'

import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, vi } from 'vitest'

import { setThemePreference } from '@/lib/theme'

import { FakeEventSource } from './eventSource'
import { setSystemDark } from './media'
import { server } from './server'

// Listen before the test file is imported: openapi-fetch keeps the fetch it finds when the
// API client module loads. Every request needs a handler, so a wrong path fails loudly.
server.listen({ onUnhandledFrame: 'error' })

globalThis.EventSource = FakeEventSource as unknown as typeof EventSource

// Monaco needs a real browser: code editors stay plain text here, which e2e tests go past.
vi.mock('@/components/code/MonacoEditor', async () => ({
  default: (await import('@/components/code/PlainEditor')).PlainEditor,
}))

afterEach(() => {
  cleanup()
  server.resetHandlers()
  FakeEventSource.open = []
  localStorage.clear() // remembered layout and theme choices
  setThemePreference('system')
  setSystemDark(false)
})

afterAll(() => server.close())
