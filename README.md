# Freeway

개인 PC에서 외부 API·공식 CLI·로컬 모델 서버를 연결하는 오픈소스 LLM 게이트웨이.

현재는 **Go 백엔드 개발 뼈대**입니다. 실행 가능한 기존 구현은 [`POC/`](POC/README.md)에 있습니다.

```text
backend/   Go 게이트웨이
cli/       CLI·TUI 개발 영역
api/       클라이언트와 서버의 API 계약
tests/     사용자 흐름·설치 검사
scripts/   패키징·설치·릴리스
docs/      구조·기획·조사
POC/       기존 Node.js 구현
```

[전체 구조](docs/development/architecture.md) · [개발 참여](CONTRIBUTING.md) · [기획](docs/planning/00-index.md)

[MIT License](LICENSE) · [라이선스 선택과 재사용 조건](docs/development/licensing.md)
