// @ts-check
import starlight from '@astrojs/starlight'
import { defineConfig } from 'astro/config'

// The docs are served by the web image's Caddy at /docs, beside the app.
export default defineConfig({
  base: '/docs',
  integrations: [
    starlight({
      title: 'Polythought',
      description: 'Workflow automation for Polymarket: build, run and share workflows.',
      customCss: ['./src/styles/custom.css'],
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
            'guides/telegram',
            'guides/copy-traders',
          ],
        },
        {
          label: 'Self-hosting',
          items: ['self-hosting/install', 'self-hosting/configuration'],
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
                ...['Triggers', 'Polymarket', 'Logic', 'Transform', 'Output', 'Telegram', 'Code', 'Trading'].map((label) => ({
                  label,
                  collapsed: true,
                  items: [{ autogenerate: { directory: `reference/nodes/${label.toLowerCase()}` } }],
                })),
              ],
            },
          ],
        },
      ],
    }),
  ],
})
