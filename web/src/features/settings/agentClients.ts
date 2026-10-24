/**
 * The steps for connecting each agents' client (D414), its address filled in. Each client
 * signs in through your browser, where this app asks what it may do.
 */

export type Step = {
  text: string
  code?: string // a command or a file's text, to copy
}

export type AgentClient = {
  id: string
  label: string
  steps: (address: string, metadata: string) => Step[]
}

const json = (v: unknown) => JSON.stringify(v, null, 2)

/** A word a shell reads as one, quoted unless it is plain (A8). */
export const shellWord = (s: string) =>
  /^[\w@%+=:,./-]+$/.test(s) ? s : `'${s.replaceAll("'", `'\\''`)}'`

/** A TOML basic string: JSON's escapes are TOML's. */
const tomlString = (s: string) => JSON.stringify(s)

export const agentClients: AgentClient[] = [
  {
    id: 'claude-code',
    label: 'Claude Code',
    steps: (address) => [
      {
        text: 'Add Polythought to Claude Code, for all your projects:',
        code: `claude mcp add --transport http --scope user polythought ${shellWord(address)}`,
      },
      {
        text: 'Sign in: in Claude Code, run /mcp, pick polythought and authenticate; your browser opens here. Outside a session, this does the same:',
        code: 'claude mcp login polythought',
      },
    ],
  },
  {
    id: 'codex',
    label: 'Codex',
    steps: (address) => [
      {
        text: 'Add it to ~/.codex/config.toml. The longer timeout lets it wait for tasks, which takes up to a minute:',
        code: `[mcp_servers.polythought]\nurl = ${tomlString(address)}\ntool_timeout_sec = 120`,
      },
      { text: 'Sign in; your browser opens here:', code: 'codex mcp login polythought' },
    ],
  },
  {
    id: 'gemini-cli',
    label: 'Gemini CLI',
    steps: (address) => [
      {
        text: 'Add Polythought to Gemini CLI, for all your projects:',
        code: `gemini mcp add --transport http --scope user polythought ${shellWord(address)}`,
      },
      {
        text: 'It signs in the first time it connects, opening your browser here; to sign in again, run this in Gemini CLI:',
        code: '/mcp auth polythought',
      },
    ],
  },
  {
    id: 'cursor',
    label: 'Cursor',
    steps: (address) => [
      {
        text: "Add it to ~/.cursor/mcp.json, for all your projects, or to a project's .cursor/mcp.json:",
        code: json({ mcpServers: { polythought: { url: address } } }),
      },
      {
        text: 'Cursor asks you to sign in when it connects, in its MCP settings; your browser opens here.',
      },
    ],
  },
  {
    id: 'opencode',
    label: 'opencode',
    steps: (address) => [
      {
        text: "Add it to opencode.json, in your project or in opencode's global config. The longer timeout lets it wait for tasks:",
        code: json({
          $schema: 'https://opencode.ai/config.json',
          mcp: { polythought: { type: 'remote', url: address, timeout: 120000 } },
        }),
      },
      {
        text: 'It signs in the first time it connects, opening your browser here; or sign in yourself:',
        code: 'opencode mcp auth polythought',
      },
    ],
  },
  {
    id: 'vscode',
    label: 'VS Code',
    steps: (address) => [
      {
        text: "Add it to your user mcp.json (the Command Palette's MCP: Open User Configuration), or a workspace's .vscode/mcp.json:",
        code: json({ servers: { polythought: { type: 'http', url: address } } }),
      },
      {
        text: 'Start it from the file; VS Code asks you to sign in, and your browser opens here. Its agent mode then has the tools.',
      },
    ],
  },
  {
    id: 'claude-ai',
    label: 'Claude Desktop and claude.ai',
    steps: (address) => [
      {
        text: "Claude connects from Anthropic's cloud, not from your computer: this works only where this address is reachable from the internet.",
      },
      {
        text: 'In Claude, open Settings, Connectors, and add a custom connector named Polythought, at this address:',
        code: address,
      },
      { text: 'Connect it; your browser opens here to allow it.' },
    ],
  },
  {
    id: 'other',
    label: 'Other clients',
    steps: (address, metadata) => [
      {
        text: 'Any client that speaks MCP over Streamable HTTP connects at this address:',
        code: address,
      },
      {
        text: 'It signs in with OAuth 2.1, registering itself or by a Client ID Metadata Document, and finds how here:',
        code: metadata,
      },
      {
        text: 'Or make an API token under API tokens, with the scopes it needs, and send it as a header:',
        code: 'Authorization: Bearer pt_…',
      },
    ],
  },
]
