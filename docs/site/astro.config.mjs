import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  output: 'static',
  trailingSlash: 'always',
  redirects: { '/': '/en/docs/', '/en/': '/en/docs/', '/ko/': '/ko/docs/' },
  integrations: [starlight({
    title: 'Freeway',
    description: 'Your model accounts. One terminal.',
    defaultLocale: 'en',
    locales: { en: { label: 'English', lang: 'en' }, ko: { label: '한국어', lang: 'ko' } },
    social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/wooinwoo/freeway-cli' }],
    customCss: ['./src/styles/custom.css'],
    components: {
      Header: './src/components/DocsHeader.astro',
      Sidebar: './src/components/DocsSidebar.astro',
      PageTitle: './src/components/DocsPageTitle.astro',
      ThemeProvider: './src/components/DocsThemeProvider.astro',
      ThemeSelect: './src/components/DocsThemeSelect.astro',
    },
    sidebar: [
      { slug: 'docs', label: 'Quickstart', translations: { ko: '빠른 시작' } },
      { label: 'Get started', translations: { ko: '시작하기' }, items: [
        { slug: 'docs/installation', label: 'Installation', translations: { ko: '설치' } },
        { slug: 'docs/connections', label: 'Provider connections', translations: { ko: '공급처 연결' } },
        { slug: 'docs/gateway', label: 'Local gateway', translations: { ko: '로컬 게이트웨이' } },
      ] },
      { label: 'Use Freeway', translations: { ko: 'Freeway 사용' }, items: [
        { slug: 'docs/models', label: 'Models and responses', translations: { ko: '모델과 응답' } },
        { slug: 'docs/sessions', label: 'Sessions and drafts', translations: { ko: '대화와 초안' } },
        { slug: 'docs/shortcuts', label: 'Keyboard shortcuts', translations: { ko: '키보드 단축키' } },
      ] },
      { label: 'Maintain', translations: { ko: '관리' }, items: [
        { slug: 'docs/updates', label: 'Updates and recovery', translations: { ko: '업데이트와 복구' } },
        { slug: 'docs/troubleshooting', label: 'Troubleshooting', translations: { ko: '문제 해결' } },
        { slug: 'docs/privacy', label: 'Privacy and usage', translations: { ko: '개인정보와 사용 조건' } },
      ] },
    ],
    expressiveCode: { themes: ['github-light', 'github-dark'], styleOverrides: { borderRadius: '0.875rem', codeFontFamily: '"Geist Mono Variable", monospace', codeFontSize: '0.875rem', codeLineHeight: '1.7', frames: { terminalTitlebarBackground: 'transparent' } } },
  })],
});
