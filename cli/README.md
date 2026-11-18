# CLI·TUI

Go 1.26 이상의 Bubble Tea TUI입니다. 제품 실행에는 Node.js와 `POC/`가 필요하지 않습니다. 화면만 보려면 저장소 루트에서 실행합니다.

```sh
go -C cli run ./cmd/freeway --preview
```

설치 후 `freeway`를 실행하면 로컬 게이트웨이에 자동으로 연결합니다. `freeway continue`로 최근 대화를 이어갑니다.

다른 프로그램에서 API를 호출하려면 한 터미널에서 `freeway gateway serve`를 실행합니다. Freeway에서 그 주소를 직접 지정하는 예는 아래와 같습니다.

```sh
export FREEWAY_API_KEY="$(freeway gateway key)"
export FREEWAY_URL=http://127.0.0.1:4141
freeway
```

PowerShell에서는 현재 셸의 `$env:FREEWAY_API_KEY`와 `$env:FREEWAY_URL`을 설정합니다. 소스 빌드에서 수동 연결을 검증하려면 `go -C backend run ./cmd/freewayd serve`와 `go -C backend run ./cmd/freewayd key`를 사용합니다.

## 사용

- Enter로 전송하고 Ctrl+J·Ctrl+O·Alt+Enter·Shift+Enter로 줄바꿈합니다.
- F2·Alt+P·`/model`에서 모델을 선택합니다. 서버가 무료이며 준비됐다고 명시한 모델만 표시합니다.
- 기본 Freeway 게이트웨이를 쓰는 경우 F2의 `OpenCode…` 또는 `/opencode`에서 설치한 OpenCode CLI의 모델을 골라 같은 채팅에서 사용합니다.
- F4 또는 `/providers`에서 공급처별 키 설정, 무료 모델과 사용량 제한을 확인합니다. Ctrl+R로 새로고침합니다.
- Esc로 생성을 중지합니다. 다음 초안은 보존합니다. Ctrl+C는 생성 중지, 입력 보관 후 지우기, 빈 입력에서 두 번 누르면 종료 순서입니다.
- Alt+H·`/resume`은 저장 대화, Alt+N·`/new`는 새 대화입니다. `freeway continue`, `freeway resume ID_OR_TITLE`, `freeway sessions [--all] [--json]`도 지원합니다.
- Ctrl+S로 초안·첨부를 보관하거나 복원합니다. Ctrl+R은 프롬프트 검색, Alt+R은 재생성, Ctrl+G는 외부 편집기입니다.
- F3 또는 `/find`로 현재 대화의 질문과 답변을 검색합니다. ↑↓로 결과를 고르고 Enter로 이동합니다.
- 위로 스크롤했을 때 입력창 위의 `↓ Jump to latest` 버튼이나 Ctrl+End로 최신 메시지로 이동합니다.
- `@`·`/attach`는 프로젝트 내부 UTF-8 파일을 첨부합니다. `@`로 파일을 고르다가 Esc를 눌러도 입력한 경로가 남습니다. 질문당 5개, 파일당 128 KB입니다.
- 생성 중 Enter·Tab은 다음 질문을 대기열에 넣습니다. `/queue pause|resume|clear`로 관리합니다.
- `/fork`는 원본을 보존해 분기합니다. `/rename TITLE`은 제목을 바꿉니다.
- `/export filename.md`는 메시지·초안·첨부·보관함·대기열을 평문 Markdown으로 내보냅니다. 기존 파일은 덮어쓰지 않습니다.
- `/copy`, `/code`, `/settings`, `/save`, `/reconnect`, `/help`를 제공합니다.

초안과 부분 응답은 스트리밍 중에도 독립 요청으로 저장합니다. 대화 전환 후 늦게 도착한 응답·저장 결과는 현재 세션에 적용하지 않습니다. revision 충돌은 덮어쓰기 대신 오류로 남깁니다. 영속화·암호화·비정상 종료 복구는 게이트웨이 책임입니다.

실패·중지된 부분 답변은 이후 요청 문맥에서 제외합니다. 선택한 경로가 출력 전에 실패하면 Auto로 한 번 재시도하며 화면에 표시합니다. 출력이 시작된 뒤에는 응답을 다른 모델과 섞지 않습니다.

## 설치와 공급처 도구

```sh
go -C cli run ./cmd/package --root "$PWD"
# 검증·설치한 패키지의 실행 진입점:
freeway providers --help
freeway providers auth kilo
freeway providers models kilo
freeway update check
freeway update
freeway rollback
freeway uninstall
```

`freeway providers`는 같은 패키지의 `freeway-providers`를 호출합니다. TUI의 대화 요청은 공급처 도구 대신 게이트웨이 API를 사용합니다. [제품 패키징](../scripts/package/README.md)을 참고하세요.

## 구조와 검증

`client/`는 HTTP·SSE와 대화 계약, `tui/`는 화면 상태, `update/`는 네이티브 버전 설치·복구입니다. `cmd/package/`는 제품 파일만 묶습니다. API 계약은 [openapi.yaml](../api/openapi.yaml)에 있습니다.

```sh
FREEWAY_LIVE=0 go -C cli test -race ./...
```

테스트는 로컬 HTTP fixture를 사용합니다. 실제 공급처·운영체제 검증과 구분합니다. 화면에 사용한 제3자 코드의 [원문 라이선스](tui/LICENSE)와 [고지](../THIRD_PARTY_NOTICES.md)를 보존합니다.
