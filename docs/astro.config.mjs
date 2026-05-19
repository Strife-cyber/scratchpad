import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  site: 'https://scratchpad.dev',
  srcDir: './src',
  outDir: './dist',
  integrations: [
    starlight({
      title: 'Scratchpad',
      description: 'Multi-platform UI automation engine documentation',
      lastUpdated: true,
      pagination: true,
      social: {
        github: 'https://github.com/Strife-cyber/scratchpad',
      },
      editLink: {
        baseUrl: 'https://github.com/Strife-cyber/scratchpad/edit/main/docs/',
      },
      sidebar: [
        {
          label: 'Getting Started',
          items: [
            { label: 'Overview', link: '/' },
            { label: 'Installation', link: '/getting-started/installation/' },
            { label: 'Quickstart', link: '/getting-started/quickstart/' },
            { label: 'Configuration', link: '/getting-started/configuration/' },
          ],
        },
        {
          label: 'Guides',
          items: [
            { label: 'Page Navigation', link: '/guides/basic-navigation/' },
            { label: 'Element Interaction', link: '/guides/element-interaction/' },
            { label: 'Assertions', link: '/guides/assertions/' },
            { label: 'Selectors', link: '/guides/selectors/' },
            { label: 'Wait Conditions', link: '/guides/wait-conditions/' },
            { label: 'Multi-Platform', link: '/guides/multi-platform/' },
            { label: 'Testing Patterns', link: '/guides/testing-patterns/' },
            { label: 'Debugging', link: '/guides/debugging/' },
          ],
        },
        {
          label: 'API Reference',
          items: [
            { label: 'WebSocket API', link: '/api/websocket/' },
            { label: 'HTTP REST API', link: '/api/rest-http/' },
            { label: 'MCP Tools', link: '/api/mcp-tools/' },
            { label: 'CLI Test Runner', link: '/api/cli-test-runner/' },
          ],
        },
        {
          label: 'Examples',
          items: [
            { label: 'Basic Click', link: '/examples/basic-click/' },
            { label: 'Login Flow', link: '/examples/login-flow/' },
            { label: 'Form Filling', link: '/examples/form-filling/' },
            { label: 'Checkout Flow', link: '/examples/checkout-flow/' },
            { label: 'Mobile Testing', link: '/examples/mobile-testing/' },
            { label: 'Flutter Testing', link: '/examples/flutter-testing/' },
            { label: 'Screenshot Testing', link: '/examples/screenshot-testing/' },
          ],
        },
        {
          label: 'Reference',
          items: [
            { label: 'Actions', link: '/reference/action-types/' },
            { label: 'Assertions', link: '/reference/assertion-types/' },
            { label: 'Observation Response', link: '/reference/observation-response/' },
            { label: 'Selectors', link: '/reference/selector/' },
            { label: 'YAML Suite Format', link: '/reference/yaml-suite-format/' },
            { label: 'Changelog', link: '/reference/changelog/' },
          ],
        },
      ],
      customCss: ['./src/assets/theme.css'],
      disable404Route: true,
    }),
  ],
});
