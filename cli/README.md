# CLI·TUI

Go 1.26 이상의 Bubble Tea TUI입니다. 제품 실행에는 Node.js와 `POC/`가 필요하지 않습니다. 화면만 보려면 저장소 루트에서 실행합니다.

```sh
go -C cli run ./cmd/freeway --preview
```

연결 모드는 API v1을 구현한 로컬 Go 게이트웨이와 발급받은 클라이언트 키가 필요합니다. 공통 게이트웨이는 현재 미구현이며 마지막 단계에서 개발합니다. TUI가 POC나 샘플 응답으로 대체하지 않습니다.

```sh
export FREEWAY_URL=http://127.0.0.1:4141
export FREEWAY_API_KEY=your-local-gateway-client-key
go -C cli run ./cmd/freeway
```

PowerShell은 `$env:FREEWAY_URL`, `$env:FREEWAY_API_KEY`를 사용합니다. `--url`은 주소를 직접 지정합니다. loopback HTTP 주소만 허용하며 리디렉션을 따라 인증키를 보내지 않습니다.

## 사용

- Enter로 전송하고 Ctrl+J·Ctrl+O·Alt+Enter·Shift+Enter로 줄바꿈합니다.
- F2·Alt+P·`/model`에서 모델을 선택합니다. 서버가 무료이며 준비됐다고 명시한 모델만 표시합니다.
- Esc로 생성을 중지합니다. 다음 초안은 보존합니다. Ctrl+C는 생성 중지, 입력 보관 후 지우기, 빈 입력에서 두 번 누르면 종료 순서입니다.
- Alt+H·`/resume`은 저장 대화, Alt+N·`/new`는 새 대화입니다. `freeway continue`, `freeway resume ID_OR_TITLE`, `freeway sessions [--all] [--json]`도 지원합니다.
- Ctrl+S로 초안·첨부를 보관하거나 복원합니다. Ctrl+R은 프롬프트 검색, Alt+R은 재생성, Ctrl+G는 외부 편집기입니다.
- `@`·`/attach`는 프로젝트 내부 UTF-8 파일을 첨부합니다. 질문당 5개, 파일당 128 KB입니다. 읽는 동안 전송을 막습니다.
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
