import { createWorkflow, expect, test, unique } from './fixtures'

test('workflows show as cards, found by their descriptions, pinned and dragged into order', async ({
  page,
}) => {
  const tag = unique('cards')
  const alpha = await createWorkflow(page, `${tag} alpha`, [
    { id: 'manual', type: 'trigger.manual' },
  ])
  const beta = await createWorkflow(page, `${tag} beta`, [{ id: 'manual', type: 'trigger.manual' }])

  try {
    // A description, written in the editor, saved with the workflow.
    await page.goto(`/workflows/${alpha}`)
    await page.getByRole('textbox', { name: 'Description' }).fill(`Finds the ${tag} needle`)
    await page.getByRole('button', { name: 'Save', exact: true }).click()
    await expect(page.getByText('Saved')).toBeVisible()

    await page.goto('/workflows')
    const search = page.getByRole('searchbox', { name: 'Search workflows' })

    await search.fill(`${tag} needle`)
    const cards = page.getByRole('list', { name: 'Workflows' })
    await expect(cards.getByRole('link', { name: `${tag} alpha` })).toBeVisible()
    await expect(cards.getByRole('link', { name: `${tag} beta` })).toHaveCount(0)
    await expect(cards).toContainText(`Finds the ${tag} needle`)

    // Pinned, after the others, each in its turn.
    await search.fill(tag)
    await page.getByRole('button', { name: `Pin ${tag} alpha` }).click()
    await page.getByRole('button', { name: `Pin ${tag} beta` }).click()
    await search.fill('')

    const pinned = page.getByRole('list', { name: 'Pinned workflows' })
    const order = () => pinned.getByRole('link').allTextContents()
    await expect.poll(order).toEqual([`${tag} alpha`, `${tag} beta`])

    // Dragged as a person drags: pressed on beta's card, moved a step at a time onto
    // alpha's place and let go there. It goes before alpha, opens nothing, and stays.
    const card = (name: string) => pinned.getByRole('listitem').filter({ hasText: name })
    const drag = async (from: { x: number; y: number }, to: { x: number; y: number }) => {
      await page.mouse.move(from.x, from.y)
      await page.mouse.down()
      await page.mouse.move(to.x, to.y, { steps: 15 })
      await page.mouse.up()
    }

    const a = (await card(`${tag} alpha`).boundingBox())!
    const b = (await card(`${tag} beta`).boundingBox())!
    await drag({ x: b.x + 40, y: b.y + b.height - 20 }, { x: a.x + 40, y: a.y + a.height - 20 })

    await expect.poll(order).toEqual([`${tag} beta`, `${tag} alpha`])
    await expect(page).toHaveURL(/\/workflows$/)

    await page.reload()
    await expect.poll(order).toEqual([`${tag} beta`, `${tag} alpha`])

    // And back, by alpha's handle, now in the second place, to the first.
    const handle = (await page.getByRole('button', { name: `Move ${tag} alpha` }).boundingBox())!
    await drag(
      { x: handle.x + handle.width / 2, y: handle.y + handle.height / 2 },
      {
        x: handle.x + handle.width / 2 - (b.x - a.x),
        y: handle.y + handle.height / 2 - (b.y - a.y),
      },
    )
    await expect.poll(order).toEqual([`${tag} alpha`, `${tag} beta`])
  } finally {
    for (const id of [alpha, beta]) {
      await page.request.put(`/api/v1/workflows/${id}/pin`, { data: { pinned: false } })
      await page.request.delete(`/api/v1/workflows/${id}`)
    }
  }
})
