import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { alice, apiError } from '@/test/fixtures'
import { server } from '@/test/server'

import { api, ApiError, call, errorMessage } from './client'

describe('ApiError', () => {
  it.each([
    {
      name: 'reads the server error',
      body: {
        error: {
          code: 'invalid_workflow',
          message: 'invalid workflow: no trigger node',
          problems: [{ message: 'no trigger node' }],
        },
      },
      want: {
        code: 'invalid_workflow',
        message: 'invalid workflow: no trigger node',
        problems: [{ message: 'no trigger node' }],
      },
    },
    {
      name: 'has no problems unless the server lists some',
      body: { error: { code: 'not_found', message: 'not found' } },
      want: { code: 'not_found', message: 'not found', problems: [] },
    },
    {
      name: 'falls back to the status for a proxy error page',
      body: '<html>Bad Gateway</html>',
      want: { code: 'http_502', message: 'The server answered 502', problems: [] },
    },
    {
      name: 'falls back for JSON of another shape',
      body: { error: 'nope' },
      want: { code: 'http_502', message: 'The server answered 502', problems: [] },
    },
  ])('$name', ({ body, want }) => {
    const status = want.code === 'http_502' ? 502 : 400
    const error = new ApiError(status, body)

    expect(error).toMatchObject({ status, ...want })
  })
})

describe('call', () => {
  it('returns the data of a successful response', async () => {
    server.use(http.get('/api/v1/me', () => HttpResponse.json(alice)))

    await expect(call(api.GET('/me'))).resolves.toEqual(alice)
  })

  it('throws an ApiError for an error response', async () => {
    server.use(http.get('/api/v1/me', () => apiError(401, 'unauthenticated', 'not authenticated')))

    await expect(call(api.GET('/me'))).rejects.toMatchObject({
      status: 401,
      code: 'unauthenticated',
    })
  })
})

describe('errorMessage', () => {
  it('capitalises the server message', () => {
    expect(
      errorMessage(new ApiError(401, { error: { code: 'x', message: 'invalid email' } })),
    ).toBe('Invalid email')
  })

  it('blames the connection when the server was not reached', () => {
    expect(errorMessage(new TypeError('Failed to fetch'))).toMatch(/could not reach the server/i)
  })
})
