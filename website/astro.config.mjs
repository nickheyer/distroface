import { defineConfig } from 'astro/config'
import starlight from '@astrojs/starlight'
import starlightLinksValidator from 'starlight-links-validator'
import { site, base, repo } from './site.config.mjs'

export default defineConfig({
  site,
  base,
  integrations: [
    starlight({
      title: 'DistroFace',
      description: 'Blob distribution and storage. Container registry, artifacts, git hosting, actions and agents in one binary.',
      logo: { light: './src/assets/logo-light.png', dark: './src/assets/logo-dark.png' },
      favicon: '/favicon.png',
      social: [{ icon: 'github', label: 'GitHub', href: repo }],
      editLink: { baseUrl: `${repo}/edit/main/docs/` },
      customCss: ['./src/styles/custom.css'],
      sidebar: [
        { label: 'Getting started', items: [{ autogenerate: { directory: 'getting-started' } }] },
        { label: 'Administration', items: [{ autogenerate: { directory: 'administration' } }] },
        { label: 'Registry', items: [{ autogenerate: { directory: 'registry' } }] },
        { label: 'Artifacts', items: [{ autogenerate: { directory: 'artifacts' } }] },
        { label: 'Git', items: [{ autogenerate: { directory: 'git' } }] },
        { label: 'Mirrors and webhooks', items: [{ slug: 'mirrors' }, { slug: 'webhooks' }] },
        { label: 'Actions', items: [{ autogenerate: { directory: 'actions' } }] },
        { label: 'Agents', items: [{ autogenerate: { directory: 'agents' } }] },
        { label: 'API', items: [{ autogenerate: { directory: 'api' } }] },
        { label: 'CLI', items: [{ autogenerate: { directory: 'cli' } }] },
        { label: 'Migration', items: [{ slug: 'migration' }] },
      ],
      plugins: [starlightLinksValidator({ errorOnLocalLinks: false })],
    }),
  ],
})
