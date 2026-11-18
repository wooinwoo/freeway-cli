# Freeway docs

Astro + Starlight 기반 정적 문서 사이트입니다. 기본 언어는 영어이며 한국어 번역을 함께 제공합니다. OpenRouter 실제 화면을 기준으로 2단 헤더·좁은 본문·아이콘 탐색·어두운 기본 테마를 구성했습니다. Starlight 기본 헤더·사이드바·제목·테마 컴포넌트를 별도 구현으로 교체했습니다.

```sh
npm ci
npm run typecheck
npm run build
npm run preview
```

출력은 `dist/`입니다. 정적 HTTP 서버로 제공하면 검색·코드 복사·언어 전환·테마가 동작합니다. 빌드 때 Node.js가 필요하며 운영 시 Node 서버는 필요하지 않습니다. 공개 도메인·외부 배포는 설정하지 않았습니다.

소스는 `src/content/docs/en/docs/`, `src/content/docs/ko/docs/`에 있습니다. 각 언어의 파일명을 맞추고 네이티브 CLI 명령과 공급처 사용 조건을 함께 갱신하세요.
