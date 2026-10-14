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
            'guides/library',
            'guides/credentials',
            'guides/telegram',
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
            {
              label: 'Nodes',
              items: [
                { label: 'All nodes', slug: 'reference/nodes' },
                // The groups of internal/tools/docgen, which writes these pages.
                ...['Triggers', 'Polymarket', 'Logic', 'Transform', 'Output'].map((label) => ({
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
