# Freeway

개인 PC에서 본인의 모델 API·공식 CLI 계정을 연결하는 오픈소스 Go 클라이언트입니다.

네이티브 패키지에는 TUI, 로컬 Go 게이트웨이, 공급처 관리 도구, 설치·업데이트 기능이 포함됩니다. 제품 실행에 Node.js가 필요하지 않습니다.

전체 설치 스크립트는 루트의 [install.sh](install.sh)와 [install.ps1](install.ps1)입니다. 지금은 Go 1.26 이상을 설치한 소스 체크아웃에서 실행합니다.

```sh
bash install.sh --source
```

Windows: `powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 -Source`. 릴리스 다운로드·로컬 패키지·사용자 지정 폴더는 [설치 안내](docs/site/src/content/docs/ko/docs/installation.mdx)를 참고하세요.

설치 후 `freeway`를 실행하면 로컬 게이트웨이가 자동으로 시작됩니다. `freeway continue`로 작업을 이어갈 수 있습니다. [게이트웨이 연결 안내](docs/site/src/content/docs/ko/docs/gateway.md)

```sh
go -C cli run ./cmd/freeway --preview
go -C cli run ./cmd/package --root "$PWD"
```

첫 명령은 계정 없이 TUI를 미리 봅니다. 두 번째는 `dist/native`에 OS·CPU별 실행 파일, 라이선스, 파일 명세와 체크섬을 묶습니다. 릴리스를 외부에 공개하지는 않습니다.

```text
cli/       Go TUI, 인증된 HTTP 클라이언트, 제품 업데이트·패키징
backend/   로컬 게이트웨이, 공급처 연동, 대화·사용량 저장
api/       백엔드 구현에 사용할 HTTP·SSE API v1 계약
docs/site/ Astro + Starlight 정적 문서 사이트
tests/     빌드된 실행 파일의 사용자 흐름 검사
POC/       기존 Node.js 구현 보관; 네이티브 제품에 미포함
```

[CLI 사용법](cli/README.md) · [전체 구조](docs/development/architecture.md) · [개발 참여](CONTRIBUTING.md) · [완료 조건과 백엔드 인계](docs/development/client-delivery.md)

[공급처 사용법](backend/internal/providers/README.md) · [공급처 API](backend/internal/providers/API.md) · [제품 패키징](scripts/package/README.md) · [공식 공급처 CLI 설치](scripts/install/README.md)

프로젝트 운영은 비상업적 개인 도구 제공을 지향합니다. 소스의 [MIT License](LICENSE)와 각 공급처 서비스의 조건은 별개입니다. [제3자 고지](THIRD_PARTY_NOTICES.md)를 보존합니다.
