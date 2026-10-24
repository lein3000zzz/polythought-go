// @ts-check
import starlight from '@astrojs/starlight'
import { defineConfig } from 'astro/config'
import rehypeMermaid from 'rehype-mermaid'

// The docs are served by the web image's Caddy at /docs, beside the app.
export default defineConfig({
  base: '/docs',
  // Diagrams are drawn when the docs build, as inline SVG: no script on the page, and the
  // theme's colours over them (custom.css), light and dark.
  markdown: {
    rehypePlugins: [[rehypeMermaid, { strategy: 'inline-svg', mermaidConfig: { theme: 'neutral' } }]],
  },
  integrations: [
    starlight({
      title: 'Polythought',
      description: 'Workflow automation for Polymarket: build, run and share workflows.',
      customCss: ['./src/styles/custom.css'],
      // The repository's link in the header, with its stars (D372).
      components: { SocialIcons: './src/components/SocialIcons.astro' },
      sidebar: [
        {
          label: 'Guides',
          items: [
            'guides/concepts',
            'guides/editor',
            'guides/expressions',
            'guides/code',
            'guides/custom-nodes',
            'guides/workflow-code',
            'guides/library',
            'guides/credentials',
            'guides/agents',
            'guides/agents-over-mcp',
            'guides/telegram',
            'guides/copy-traders',
          ],
        },
        {
          label: 'Self-hosting',
          items: [
            'self-hosting/install',
            'self-hosting/configuration',
            'self-hosting/plugins',
            'self-hosting/write-a-plugin',
          ],
        },
        {
          label: 'Reference',
          items: [
            'reference/settings',
            'reference/credential-types',
            'reference/code-types',
            {
              label: 'Nodes',
              items: [
                { label: 'All nodes', slug: 'reference/nodes' },
                // The groups of tools/docgen, which writes these pages.
                ...['Triggers', 'Polymarket', 'Logic', 'Transform', 'Output', 'Telegram', 'Code', 'Trading', 'AI'].map((label) => ({
                  label,
                  collapsed: true,
                  items: [{ autogenerate: { directory: `reference/nodes/${label.toLowerCase()}` } }],
                })),
              ],
            },
          ],
        },
        {
          label: 'Architecture',
          items: [
            'architecture/overview',
            'architecture/engine',
            'architecture/user-code',
            'architecture/plugins',
            'architecture/trading',
            'architecture/agents',
            'architecture/security',
            'architecture/data',
            'architecture/messaging',
            'architecture/observability',
            'architecture/web',
            'architecture/decisions',
          ],
        },
      ],
    }),
  ],
})
