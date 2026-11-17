# Freeway

개인 PC에서 외부 API·공식 CLI·로컬 모델 서버를 연결하는 오픈소스 LLM 게이트웨이.

**[Go TUI](cli/README.md)를 기존 [POC](POC/README.md)에 임시 연결**해 무료 모델로 대화합니다. Go 백엔드는 개발 뼈대입니다.

무료 경로: Kilo API · OpenCode CLI · Cline CLI. 공급처별 로그인과 무료 할당량이 적용됩니다.

```text
backend/   Go 게이트웨이
cli/       Go TUI·임시 POC 클라이언트
api/       클라이언트와 서버의 API 계약
tests/     사용자 흐름·설치 검사
scripts/   패키징·설치·릴리스
docs/      구조·기획·조사
POC/       기존 Node.js 구현
```

[전체 구조](docs/development/architecture.md) · [개발 참여](CONTRIBUTING.md) · [기획](docs/planning/00-index.md)

[공급처별 인증·연결·모델 시험 명령](backend/internal/providers/README.md)

[백엔드 연동 방법과 Go 호출 예제](backend/internal/providers/INTEGRATION.md)

[MIT License](LICENSE) · [라이선스 선택과 재사용 조건](docs/development/licensing.md)
