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
- F2·Alt+P·`/model`에서 모델을 선택합니다. 목록에서 Ctrl+T를 누르면 종합·코딩·추론 순위가 바뀌고 하단에 비교 근거가 표시됩니다. Auto는 코딩이나 증명·유도 요청을 감지하면 해당 비교를 반영합니다.
- 같은 모델을 제공하는 무료 경로는 한 항목에 표시합니다. 응답을 시작하기 전에 연결이 실패하면 다른 공급처를 시도할 수 있습니다. 자동 게이트웨이에서는 `/opencode`로 OpenCode 모델을 검색합니다.
- 채팅 상단에 연결된 공급처가 표시됩니다. F4 또는 `/providers`에서 공급처별 연결 상태, 등록된 키, 무료 모델과 사용량 제한을 확인합니다. Ctrl+R로 새로고침합니다.
- 공급처를 선택하고 Enter를 누르거나 `freeway connect`를 실행하면 공식 로그인 또는 API 키 발급·등록 안내가 열립니다. 연결된 모델이 없을 때는 이 안내가 채팅보다 먼저 열립니다.
- `/language`에서 한국어·영어를 선택합니다. `/theme`는 채팅을 보여 주면서 ↑↓로 색상을 즉시 미리 봅니다. Enter로 저장하거나 Esc로 취소합니다.
- 설치된 CLI에서 대화형 기본 게이트웨이를 시작할 때 최신 릴리스를 확인합니다. 새 버전이 있으면 업데이트 후 시작하거나 현재 버전으로 계속할 수 있습니다.
- Esc로 생성을 중지합니다. 다음 초안은 보존합니다. Ctrl+C는 생성 중지, 입력 보관 후 지우기, 빈 입력에서 두 번 누르면 종료 순서입니다.
- Alt+H·`/resume`은 저장 대화, Alt+N·`/new`는 새 대화입니다. `freeway continue`, `freeway resume ID_OR_TITLE`, `freeway sessions [--all] [--json]`도 지원합니다.
- Ctrl+S로 초안·첨부를 보관하거나 복원합니다. Ctrl+R은 프롬프트 검색, Alt+R은 재생성, Ctrl+G는 외부 편집기입니다.
- F3 또는 `/find`로 현재 대화의 질문과 답변을 검색합니다. ↑↓로 결과를 고르고 Enter로 이동합니다.
- 위로 스크롤했을 때 입력창 위의 `↓ Jump to latest` 버튼이나 Ctrl+End로 최신 메시지로 이동합니다.
- `@`·`/attach`는 프로젝트 내부 UTF-8 파일을 첨부합니다. `@`로 파일을 고르다가 Esc를 눌러도 입력한 경로가 남습니다. 질문당 5개, 파일당 128 KB입니다.
- 생성 중 Enter·Tab은 다음 질문을 대기열에 넣습니다. `/queue pause|resume|clear`로 관리합니다. `clear`를 확인하면 예약한 질문과 첨부만 삭제합니다.
- `/fork`는 원본을 보존해 분기합니다. `/rename TITLE`은 제목을 바꿉니다.
- `/export filename.md`는 메시지·초안·첨부·보관함·대기열을 평문 Markdown으로 내보냅니다. 기존 파일은 덮어쓰지 않습니다.
- `/copy`, `/code`, `/settings`, `/save`, `/reconnect`, `/help`를 제공합니다.
- F6에서 Streamable HTTP 이미지 MCP 도구를 최대 세 개 연결하고 우선순위를 바꿉니다. F6을 열면 도구별 연결 상태를 확인합니다. 다음 도구로 자동 전환하는 시점은 생성 호출 전까지입니다. HTTPS와 동적 OAuth 등록을 지원하는 서버에서는 브라우저로 승인할 수 있습니다. 서버가 갱신 토큰을 발급하면 만료된 로그인을 자동으로 갱신합니다. Bearer 인증은 `FREEWAY_MCP_IMAGE_TOKEN` 같은 환경변수 이름을 입력합니다. 터미널에서도 `freeway image status`로 확인할 수 있습니다.
- `/mcp` 또는 `freeway mcp manage`에서 일반 MCP 서버를 저장하고 연결 상태를 확인합니다. 브라우저 로그인과 Bearer 환경변수를 지원합니다. `freeway mcp connect`는 서버 추가로 바로 이동합니다.
- `/mcp-ask 질문`은 현재 모델에게 질문과 저장된 일반 MCP 도구 요약을 보냅니다. 표시된 서버·도구·인수를 확인한 뒤 F8로 승인하거나 Esc로 취소합니다. 승인하면 도구를 한 번 호출하고 텍스트 결과를 선택한 모델의 답변에 사용합니다.
- `freeway mcp tools`는 저장한 서버와 F9 연결의 도구와 입력 스키마를 함께 조회합니다. `freeway mcp call --server ID --args-file arguments.json TOOL`로 조회 결과의 서버와 도구를 지정합니다. `--args-file -`는 표준 입력에서 JSON 객체를 읽습니다. 세션에서만 쓸 서버는 `--url URL`로 지정하고, 필요한 경우 `--token-env FREEWAY_MCP_TOKEN`을 함께 사용합니다. 명령은 터미널에 JSON 결과를 출력합니다.

초안과 부분 응답은 스트리밍 중에도 독립 요청으로 저장합니다. 대화 전환 후 늦게 도착한 응답·저장 결과는 현재 세션에 적용하지 않습니다. revision 충돌은 덮어쓰기 대신 오류로 남깁니다. 영속화·암호화·비정상 종료 복구는 게이트웨이 책임입니다.

실패·중지된 부분 답변은 이후 요청 문맥에서 제외합니다. 직접 선택한 모델에서 오류가 나면 선택을 유지합니다. 같은 모델은 Alt+R로 재시도하고, Auto로 보내려면 `/auto`를 실행한 뒤 Alt+R을 누릅니다.

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
