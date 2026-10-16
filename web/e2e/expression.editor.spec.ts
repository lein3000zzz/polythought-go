import { createWorkflow, expect, test, unique } from './fixtures'

test('expressions complete from the fields there, and what TypeScript finds wrong is underlined', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('typed'), [
    { id: 'manual', type: 'trigger.manual' },
    { id: 'markets', type: 'polymarket.markets' },
    { id: 'log', type: 'output.log' },
  ])

  await page.goto(`/workflows/${id}`)
  await page.locator('.react-flow__node[data-id="log"]').click()

  const settings = page.getByRole('complementary', { name: 'Settings' })
  const message = settings.getByRole('textbox', { name: 'Message' })
  await expect(settings.locator('.monaco-editor')).toBeVisible()
  await message.focus()

  // The Markets node's items have a question.
  await page.keyboard.type('Ask: {{ ques')
  const suggestions = page.locator('.suggest-widget')
  await expect(suggestions.getByText('question', { exact: true })).toBeVisible()
  await page.keyboard.press('Enter')
  await page.keyboard.type(' * 2 }}')

  // Monaco draws spaces as non-breaking ones.
  await expect(settings.locator('.view-lines')).toHaveText(/^Ask:\s\{\{\squestion\s\*\s2\s\}\}$/)
  await expect(settings.locator('.squiggly-error')).toBeVisible({ timeout: 20_000 })

  // Keys in the field are the field's: Backspace edits it, not the canvas's selection.
  await page.keyboard.press('Backspace')
  await page.keyboard.type('}')
  await expect(page.locator('.react-flow__node[data-id="log"]')).toBeVisible()

  // Shown, not blocking: it compiles, so it saves.
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Saved', { exact: true })).toBeVisible()
})
