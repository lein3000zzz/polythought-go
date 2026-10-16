import { expect, test, unique } from './fixtures'

test('code is written in Monaco, typed against the SDK; type errors show but do not stop saving', async ({
  page,
}) => {
  await page.goto('/nodes/new')

  const editor = page.locator('.monaco-editor').first()
  await expect(editor).toBeVisible()
  await expect(editor.locator('.view-lines')).toContainText('export function run')

  // The starter type-checks against the SDK; one more function that does not, does not.
  await editor.click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.insertText(
    '\nexport function more(ctx: Context) {\n  return ctx.execution.nope\n}\n',
  )

  const errors = page.getByText(/^1 type error:/)
  await expect(errors).toBeVisible({ timeout: 20_000 })
  await errors.click()
  await expect(page.getByText(/Property 'nope' does not exist on type 'Execution'/)).toBeVisible()

  const name = unique('typed')
  await page.getByRole('textbox', { name: 'Name' }).fill(name)
  await page.getByRole('textbox', { name: 'Label' }).fill('Typed')
  await page.getByRole('button', { name: 'Make the node' }).click()
  await expect(page.getByText(`Made custom.${name}`)).toBeVisible()
})
