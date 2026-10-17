import { canvasNode, createWorkflow, expect, test, unique } from './fixtures'

// Checksummed, as Polymarket writes them; the typo has one letter's case off.
const alice = '0x5f3C00000000000000000000000000000000A1b2'
const bob = '0x91D000000000000000000000000000000000C7e4'
const bobTypo = '0x91d000000000000000000000000000000000C7e4'

test('traders are followed from a table, edited in a dialog and checked when saved', async ({
  page,
}) => {
  const id = await createWorkflow(page, unique('traders'), [
    {
      id: 'copy',
      type: 'trigger.walletActivity',
      params: { traders: [{ label: 'Alice', address: alice }] },
    },
    {
      id: 'log',
      type: 'output.log',
      params: { message: '{{ trader.label }} {{ side }} {{ dollars }}' },
    },
  ])
  await page.goto(`/workflows/${id}`)
  await canvasNode(page, 'copy').click()

  const settings = page.getByRole('complementary', { name: 'Settings' })
  await expect(settings.getByText('1 row: Alice')).toBeVisible()
  await settings.getByRole('button', { name: 'Edit traders' }).click()

  const dialog = page.getByRole('dialog', { name: 'Traders' })
  await dialog.getByRole('button', { name: 'Add a row' }).click()
  await dialog.getByRole('textbox', { name: 'Label, row 2' }).fill('Bob')
  await dialog.getByRole('textbox', { name: 'Address, row 2' }).fill(bobTypo)
  await dialog.getByRole('textbox', { name: 'Size ×, row 2' }).fill('0.5')
  await dialog.getByRole('checkbox', { name: 'On, row 1' }).click() // Alice off
  await dialog.getByRole('button', { name: 'Done' }).click()
  await expect(settings.getByText('2 rows: Alice, Bob')).toBeVisible()

  // Saving finds the typo the address's checksum gives away.
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(settings.getByRole('alert')).toContainText('has a typo')

  await settings.getByRole('button', { name: 'Edit traders' }).click()
  await dialog.getByRole('textbox', { name: 'Address, row 2' }).fill(bob)
  await dialog.getByRole('button', { name: 'Done' }).click()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Version 2')).toBeVisible()

  const saved = (await (await page.request.get(`/api/v1/workflows/${id}`)).json()) as {
    nodes: { params?: Record<string, unknown> }[]
  }
  expect(saved.nodes[0].params?.traders).toEqual([
    { label: 'Alice', address: alice, on: false },
    { label: 'Bob', address: bob, scale: '0.5' },
  ])
})
