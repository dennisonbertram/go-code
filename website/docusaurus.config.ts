import { themes as prismThemes } from 'prism-react-renderer';
import type { Config } from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

// Tailwind CSS + autoprefixer plugin for Docusaurus
function tailwindPlugin() {
  return {
    name: 'tailwind-plugin',
    configurePostCss(postcssOptions: { plugins: unknown[] }) {
      postcssOptions.plugins.push(require('tailwindcss'));
      postcssOptions.plugins.push(require('autoprefixer'));
      return postcssOptions;
    },
  };
}

// Geist dark Prism theme (tuned to Geist palette)
import type { PrismTheme } from 'prism-react-renderer';

const geistDarkTheme: PrismTheme = {
  plain: {
    color: '#ededed',
    backgroundColor: '#0a0a0a',
  },
  styles: [
    { types: ['comment', 'prolog', 'doctype', 'cdata'], style: { color: '#878787' } },
    { types: ['punctuation'], style: { color: '#a0a0a0' } },
    { types: ['property', 'tag', 'boolean', 'number', 'constant', 'symbol'], style: { color: '#47a8ff' } },
    { types: ['selector', 'attr-name', 'string', 'char', 'builtin'], style: { color: '#75d7ff' } },
    { types: ['operator', 'entity', 'url'], style: { color: '#a0a0a0' } },
    { types: ['atrule', 'attr-value', 'keyword'], style: { color: '#0090ff' } },
    { types: ['function', 'class-name'], style: { color: '#47a8ff' } },
    { types: ['regex', 'important', 'variable'], style: { color: '#ffa600' } },
    { types: ['inserted'], style: { color: '#28a948', background: 'rgba(40,169,72,0.15)' } },
    { types: ['deleted'], style: { color: '#fc0035', background: 'rgba(252,0,53,0.12)' } },
  ],
};

const config: Config = {
  title: 'go-code',
  tagline: 'A high-performance agentic coding harness, rebuilt in Go for parallel agent runs.',
  favicon: 'img/favicon.ico',

  future: {
    v4: true,
  },

  url: 'https://dennisonbertram.github.io',
  baseUrl: '/go-code/',

  organizationName: 'dennisonbertram',
  projectName: 'go-code',
  trailingSlash: false,

  onBrokenLinks: 'throw',
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'warn',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  // Load Geist fonts from Vercel's CDN
  headTags: [
    {
      tagName: 'link',
      attributes: {
        rel: 'preconnect',
        href: 'https://fonts.googleapis.com',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'preconnect',
        href: 'https://fonts.gstatic.com',
        crossorigin: 'anonymous',
      },
    },
    {
      tagName: 'link',
      attributes: {
        rel: 'stylesheet',
        href: 'https://fonts.googleapis.com/css2?family=Geist:wght@300;400;500;600;700&family=Geist+Mono:wght@400;500&display=swap',
      },
    },
  ],

  plugins: [tailwindPlugin],

  themes: ['@docusaurus/theme-live-codeblock'],

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          editUrl: 'https://github.com/dennisonbertram/go-code/tree/main/website/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/docusaurus-social-card.jpg',
    colorMode: {
      defaultMode: 'dark',
      respectPrefersColorScheme: true,
    },
    navbar: {
      title: 'go-code',
      hideOnScroll: false,
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'tutorialSidebar',
          position: 'left',
          label: 'Docs',
        },
        {
          href: 'https://github.com/dennisonbertram/go-code',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Documentation',
          items: [
            {
              label: 'Getting Started',
              to: '/docs/getting-started/what-is-go-code',
            },
          ],
        },
        {
          title: 'Project',
          items: [
            {
              label: 'GitHub',
              href: 'https://github.com/dennisonbertram/go-code',
            },
          ],
        },
      ],
      copyright: `© ${new Date().getFullYear()} go-code`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: geistDarkTheme,
      additionalLanguages: ['bash', 'go', 'json', 'typescript', 'tsx', 'yaml', 'toml', 'diff'],
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
