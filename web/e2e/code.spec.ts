import { expect, test } from './fixtures'

test('the server hands the editor the types code is written against', async ({ page }) => {
  const res = await page.request.get('/api/v1/code/types')
  expect(res.status()).toBe(200)

  const { files } = (await res.json()) as { files: { path: string; content: string }[] }
  expect(files.map((f) => f.path)).toEqual(['index.d.ts', 'sdk.d.ts', 'nodes.d.ts'])

  const nodes = files[2].content
  expect(nodes).toContain('export interface PolymarketMarketsItem {')
  expect(nodes).toContain('export interface TelegramAskSettings {')
  expect(nodes).toContain(
    [
      '    "logic.if": {',
      '        settings: LogicIfSettings;',
      '        items: "received";',
      '        trigger: false;',
      '        inputs: "main";',
      '        outputs: "true" | "false";',
      '    };',
    ].join('\n'),
  )
  expect(nodes).toContain('        outputs: "timeout" | (string & {});') // telegram.ask's buttons
  expect(files[1].content).toContain('export declare function dec(n: DecimalLike): Decimal;')
})
