import { test as base, expect, type Page } from '@playwright/test'

export { expect }

export type Account = { email: string; password: string }

/** The admin the stack creates on its first start, from POLYTHOUGHT_ADMIN_*. */
export const admin: Account = {
  email: process.env.E2E_ADMIN_EMAIL ?? 'admin@e2e.test',
  password: process.env.E2E_ADMIN_PASSWORD ?? 'e2e-admin-password',
}

/** Where the setup project keeps the admin's session for the tests to start from. */
export const adminState = 'e2e/.auth/admin.json'

/** For tests that start signed out. */
export const signedOut = { cookies: [], origins: [] }

/** A name no other test or run has used, as tests share one database. */
export function unique(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
}

// Failed requests the app handles, like a 401 before signing in, are logged by the
// browser too; those are expected. Anything else in the console fails the test, CSP
// violations included.
const handledHttpError = /^Failed to load resource: the server responded with a status of 4\d\d/

export const test = base.extend<{ console: void; expectedConsoleError?: RegExp }>({
  // Further console errors a test expects, set with test.use.
  expectedConsoleError: [undefined, { option: true }],

  console: [
    async ({ page, expectedConsoleError }, use) => {
      const problems: string[] = []
      const expected = (text: string) =>
        handledHttpError.test(text) || (expectedConsoleError?.test(text) ?? false)

      page.on('console', (m) => {
        if (m.type() === 'error' && !expected(m.text())) problems.push(m.text())
      })
      page.on('pageerror', (e) => problems.push(e.message))

      await use()

      expect(problems, 'errors in the browser console').toEqual([])
    },
    { auto: true },
  ],
})

export async function signIn(page: Page, account: Account) {
  await page.goto('/login')
  await page.getByLabel('Email').fill(account.email)
  await page.getByLabel('Password').fill(account.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('button', { name: 'Sign out' })).toBeVisible()
}

type NodeSpec = { id: string; type: string; params?: Record<string, unknown> }

/**
 * Creates a workflow through the API as the page's signed-in user: a chain of nodes,
 * each connected to the next by its main port.
 */
export async function createWorkflow(page: Page, name: string, chain: NodeSpec[]): Promise<string> {
  const res = await page.request.post('/api/v1/workflows', {
    data: {
      name,
      nodes: chain.map((n, i) => ({ ...n, position: { x: i * 300, y: 0 } })),
      connections: chain.slice(1).map((n, i) => ({
        from: chain[i].id,
        fromPort: 'main',
        to: n.id,
        toPort: 'main',
      })),
    },
  })
  expect(res.status(), await res.text()).toBe(201)

  return ((await res.json()) as { id: string }).id
}

/** Drags from one node's output port to another's input port on the canvas. */
export async function connect(page: Page, from: string, fromPort: string, to: string) {
  const handle = (id: string, port: string, kind: 'source' | 'target') =>
    page.locator(
      `.react-flow__node[data-id="${id}"] .react-flow__handle.${kind}[data-handleid="${port}"]`,
    )

  const a = await handle(from, fromPort, 'source').boundingBox()
  const b = await handle(to, 'main', 'target').boundingBox()
  if (!a || !b) throw new Error(`no handles to connect ${from}.${fromPort} -> ${to}`)

  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2)
  await page.mouse.down()
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 10 })
  await page.mouse.up()
}

export function canvasNode(page: Page, id: string) {
  return page.locator(`.react-flow__node[data-id="${id}"]`)
}
