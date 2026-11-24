# 개발 참여

현재 구현은 Go TUI, 로컬 Go 게이트웨이, 네이티브 공급처 어댑터·도구, 설치·업데이트와 Astro 정적 문서입니다. [구조와 책임](docs/development/architecture.md), [클라이언트 완료 조건](docs/development/client-delivery.md)을 기준으로 작업합니다.

## 검사

Go 1.26 이상에서 저장소 루트를 기준으로 실행합니다. 실제 모델 호출은 기본 검사에 포함하지 않습니다.

```sh
FREEWAY_LIVE=0 go -C cli test -race ./...
FREEWAY_LIVE=0 go -C backend test -race ./...
go -C cli run ./cmd/package --root "$PWD"
python3 tests/install/release_smoke.py dist/native --go go
python3 tests/e2e/native_smoke.py "dist/native/freeway_$(cat VERSION)_linux_amd64.tar.gz"
python3 tests/install/bootstrap_smoke.py --go /absolute/path/to/go
```

실행 파일 시험의 아카이브 버전은 `VERSION`과 맞춰 사용합니다. 로컬 HTTP fixture를 열 수 있는 환경이 필요합니다. 공급처 실계정 시험은 별도로 실행하며 실제 할당량을 소비합니다.

배포용 패키지는 서명 키를 지정해 빌드합니다. `FREEWAY_RELEASE_SIGNING_KEY`에 Ed25519 시드(base64) 파일 경로를 넣으면 `dist/native/release.json.sig`가 함께 만들어집니다. 키 파일은 저장소 밖에 두고, 공개키는 `cli/update/signature.go`의 `releaseKeys`와 맞아야 합니다. 클라이언트는 HTTPS 릴리스 명세에 서명이 없으면 거부합니다. 서명 없는 명세는 루프백 HTTP 주소(시험용)에서만 받습니다.

```sh
FREEWAY_RELEASE_SIGNING_KEY=/path/outside/repo/release-signing.key go -C cli run ./cmd/package --root "$PWD"
```

Linux에서는 언어·이미지·공급처·MCP 흐름까지 시험합니다. macOS와 Windows는 설정 폴더가 `XDG_CONFIG_HOME`을 따르지 않으므로, 복구·문맥·기록·재시작 흐름만 시험합니다.

[사전 검증 워크플로](.github/workflows/preflight.yml)는 패키지 6종을 한 번 빌드하고 동일한 파일을 Linux amd64, macOS arm64, Windows amd64에서 검증합니다. 세 환경 모두 설치된 TUI에서 스트리밍·중단·초안 복원을 시험합니다. Windows에서는 ConPTY와 PowerShell 설치도 검사합니다. 워크플로에서 만든 패키지는 Actions 아티팩트로 확인할 수 있습니다.

```sh
npm --prefix docs/site ci
npm --prefix docs/site run typecheck
npm --prefix docs/site run build
```

문서 빌드에만 Node.js가 필요합니다. 제품 배포는 Go 실행 파일·명세·라이선스만 포함합니다. 교차 컴파일은 다른 운영체제에서 실행됐다는 증거가 아닙니다.

## 기여 범위

단위 테스트는 Go 구현 옆의 `*_test.go`에 둡니다. 빌드된 제품 검사는 `tests/e2e/`, 문서 소스는 `docs/site/src/content/docs/`를 사용합니다. 백엔드 정책을 TUI나 공급처 어댑터에 중복 구현하지 않습니다.

자신이 제공할 권리가 있는 코드와 문서만 제출합니다. 본인 기여분은 [MIT License](LICENSE)로 제공합니다. 외부 코드의 출처·버전·라이선스·수정 범위를 밝히고 원문 고지를 보존합니다. [제3자 고지](THIRD_PARTY_NOTICES.md)를 함께 갱신합니다.

키·토큰·대화 원문·사용자 DB·모델 가중치·클론한 타 프로젝트·빌드 산출물은 커밋하지 않습니다. `POC/`는 보관용 기존 구현입니다.
