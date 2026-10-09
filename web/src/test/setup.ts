import '@testing-library/jest-dom/vitest'

import { cleanup } from '@testing-library/react'
import { afterAll, afterEach } from 'vitest'

import { server } from './server'

// Listen before the test file is imported: openapi-fetch keeps the fetch it finds when the
// API client module loads. Every request needs a handler, so a wrong path fails loudly.
server.listen({ onUnhandledFrame: 'error' })

afterEach(() => {
  cleanup()
  server.resetHandlers()
})

afterAll(() => server.close())
