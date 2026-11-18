# Freeway

개인 PC에서 본인의 모델 API·공식 CLI 계정을 연결하는 오픈소스 Go 클라이언트입니다.

현재 구성은 **네이티브 TUI·공급처 어댑터·Astro 문서 사이트·설치/업데이트**입니다. 제품 실행에 Node.js나 기존 POC가 필요하지 않습니다. **공통 Go 게이트웨이는 현재 미구현이며 마지막 단계에서 개발합니다.** 지금은 오프라인 미리보기와 공급처 도구를 사용할 수 있습니다. 연결 대화에는 API v1을 구현한 로컬 서버가 필요합니다.

```sh
go -C cli run ./cmd/freeway --preview
go -C cli run ./cmd/package --root "$PWD"
```

첫 명령은 계정 없이 TUI를 미리 봅니다. 두 번째는 `dist/native`에 OS·CPU별 실행 파일, 라이선스, 파일 명세와 체크섬을 묶습니다. 릴리스를 외부에 공개하지는 않습니다.

```text
cli/       Go TUI, 인증된 HTTP 클라이언트, 제품 업데이트·패키징
backend/   네이티브 공급처 어댑터·도구와 공통 게이트웨이 개발 영역
api/       백엔드 구현에 사용할 HTTP·SSE API v1 계약
docs/site/ Astro + Starlight 정적 문서 사이트
tests/     빌드된 실행 파일의 사용자 흐름 검사
POC/       기존 Node.js 구현 보관; 네이티브 제품에 미포함
```

[CLI 사용법](cli/README.md) · [전체 구조](docs/development/architecture.md) · [개발 참여](CONTRIBUTING.md) · [완료 조건과 백엔드 인계](docs/development/client-delivery.md)

[공급처 사용법](backend/internal/providers/README.md) · [공급처 API](backend/internal/providers/API.md) · [제품 패키징](scripts/package/README.md) · [공식 공급처 CLI 설치](scripts/install/README.md)

프로젝트 운영은 비상업적 개인 도구 제공을 지향합니다. 소스의 [MIT License](LICENSE)와 각 공급처 서비스의 조건은 별개입니다. [제3자 고지](THIRD_PARTY_NOTICES.md)를 보존합니다.
