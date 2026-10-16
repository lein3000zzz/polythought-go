import { createWorkflow, expect, signedOut, signIn, test, unique } from './fixtures'

type Execution = { status: string; error?: string; runs: unknown[] }

test('code for chosen users: others cannot save or run it until an admin picks them', async ({
  page,
  browser,
}) => {
  const person = { email: `${unique('coder')}@e2e.test`, password: 'coder-password-123' }
  const created = await page.request.post('/api/v1/admin/users', { data: person })
  expect(created.status(), await created.text()).toBe(201)

  const theirs = await browser.newContext({ storageState: signedOut })
  const them = await theirs.newPage()
  await signIn(them, person)

  // Code is for everyone by default, so they make a workflow with a code node.
  const id = await createWorkflow(them, unique('code'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'make', type: 'code.transform' },
  ])
  const run = async () => {
    const res = await them.request.post(`/api/v1/workflows/${id}/run`)
    expect(res.status()).toBe(200)
    return (await res.json()) as Execution
  }
  expect((await run()).status).toBe('succeeded')

  const setAccess = (value: string | null) =>
    page.request.patch('/api/v1/admin/settings', { data: { values: { 'code.access': value } } })

  expect((await setAccess('chosen')).status()).toBe(200)

  try {
    // Not picked: it fails at once, before any node runs.
    const refused = await run()
    expect(refused.status).toBe('failed')
    expect(refused.error).toBe(
      'an admin has turned code off for you, so the workflow does not run: it has code nodes (make)',
    )
    expect(refused.runs).toEqual([])

    // And it cannot be saved.
    const saved = await them.request.get(`/api/v1/workflows/${id}`)
    const { name, nodes, connections, version } = (await saved.json()) as Record<string, unknown>
    const update = await them.request.put(`/api/v1/workflows/${id}`, {
      data: { name, nodes, connections, version },
    })
    expect(update.status()).toBe(400)
    expect(await update.text()).toContain('node \\"make\\": an admin has turned code off for you')

    // The editor says so.
    await them.goto(`/workflows/${id}`)
    await expect(them.getByText('An admin has turned code off for you.')).toBeVisible()
    await expect(them.getByRole('button', { name: /Code: transform/ })).toBeDisabled()
    await expect(them.getByText('Code is off for you')).toBeVisible()

    // An admin picks them on the Users page.
    await page.goto('/admin')
    await expect(
      page.getByText(/Admins may write code, and the users switched on below/),
    ).toBeVisible()
    await page.getByRole('switch', { name: `${person.email} may write code` }).click()
    await expect(page.getByText(`${person.email} may write code`, { exact: true })).toBeVisible()

    expect((await run()).status).toBe('succeeded')

    await them.reload()
    await expect(them.getByRole('button', { name: /Code: transform/ })).toBeEnabled()
    await expect(them.getByText('Code is off for you')).toBeHidden()
  } finally {
    expect((await setAccess(null)).status()).toBe(200)
    await theirs.close()
  }
})
