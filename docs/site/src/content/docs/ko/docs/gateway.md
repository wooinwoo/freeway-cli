---
title: 로컬 게이트웨이
description: 터미널 작업 공간을 로컬 API v1 서버에 연결합니다.
---

## 연결

`freeway`를 실행하면 로컬 게이트웨이를 자동으로 시작하거나 열린 게이트웨이에 연결합니다. 자동 연결은 비어 있는 루프백 포트를 사용합니다. 대화와 사용량은 `~/.freeway/gateway`에 저장하며, `FREEWAY_GATEWAY_HOME`으로 별도 저장 경로를 쓸 수 있습니다.

## API 클라이언트용 고정 주소

한 터미널에서 게이트웨이를 시작합니다. 기본 주소는 `127.0.0.1:4141`이고 대화와 사용량은 `~/.freeway/gateway`에 저장합니다.

```sh
freeway gateway serve
```

다른 터미널에서 클라이언트 키를 설정하고 Freeway를 실행합니다.

```sh
export FREEWAY_API_KEY="$(freeway gateway key)"
export FREEWAY_URL=http://127.0.0.1:4141
freeway
```

```powershell
$env:FREEWAY_API_KEY = (freeway gateway key)
$env:FREEWAY_URL = 'http://127.0.0.1:4141'
freeway
```

4141 포트가 사용 중이라면 `freeway gateway serve`에 `--listen 127.0.0.1:4142`를 주고, 클라이언트 터미널에서 `FREEWAY_URL`을 `http://127.0.0.1:4142`로 설정합니다. 저장 경로를 바꿀 때는 `serve`와 `key`에 같은 `--data-dir`을 전달하세요.

모델 공급처 인증은 `freeway providers auth 공급처ID`로 설정합니다. 자동 게이트웨이는 직접 선택한 모델의 공급처 후보에 설치된 OpenCode CLI를 포함합니다. OpenCode·Cline·NVIDIA는 공급처 도구로도 직접 호출할 수 있습니다. Auto는 사용 가능한 HTTP 공급처 중에서 선택합니다.

`/model`에서 준비된 모델을 선택하고 질문을 보냅니다. Esc로 응답을 취소할 수 있습니다. `freeway sessions`로 프로젝트 세션을 찾고 `freeway resume SESSION_ID`로 이어갑니다.

자동 선택의 첫 시도는 지정된 우선순위가 없으면 성능순을 따릅니다. 같은 모델을 지원하는 공급처 중 현재 사용 가능한 곳은 F2에 표시되며, 완료된 답변 아래에서 실제로 응답한 공급처와 모델을 확인할 수 있습니다.

## 이미지 생성

F9 또는 `/image-setup`으로 신뢰하는 Streamable HTTP MCP 서버의 URL을 입력합니다. 이미지 생성 도구를 선택한 뒤, 과금이 발생하지 않는 것을 확인하고 Auto에 연결합니다. 도구는 문자열 `prompt`를 입력받아 MCP 이미지 첨부 또는 내장 리소스로 PNG나 JPEG를 반환해야 합니다.

Auto에서 “검은 배경의 고양이 로고 만들어줘”라고 요청하거나, 모델과 관계없이 `/image 고양이 로고`로 이미지 생성을 지정합니다. Freeway는 생성한 파일을 사용자 설정 폴더의 `freeway/images`에 저장합니다. `/image-open`으로 최근 이미지를 열고 `/image-path`로 경로를 복사할 수 있습니다.

F9에서 이미지 도구를 최대 세 개 연결하고, 먼저 사용할 도구를 고를 수 있습니다. Auto는 생성 요청을 보내기 전에 연결이 끊겼거나 선택한 도구가 사라졌으면 다음 도구를 확인합니다. 생성 호출을 시작한 뒤 결과가 불확실하면 다른 도구로 재전송하지 않습니다. 이미지 아래에 실제로 사용한 서버와 도구를 표시합니다.

`freeway image status`로 연결한 도구의 상태를 확인합니다. 상태 확인은 이미지 생성을 호출하지 않습니다. F9에서 개별 연결을 해제하고 `freeway image disconnect`로 모두 해제할 수 있습니다. 이미 생성한 파일은 계속 열 수 있습니다.

F9의 **브라우저로 로그인**은 OAuth 동적 클라이언트 등록을 지원하는 공개 HTTPS MCP 서버에서 사용할 수 있습니다. 로그인과 토큰 발급은 같은 HTTPS 출처에서 이루어져야 합니다. 로그인 사이트와 요청 권한을 보고 승인한 뒤 이미지 도구를 선택합니다. Freeway는 토큰을 해당 MCP 주소에 묶어 암호화해 저장합니다. 공개 OAuth 클라이언트에 갱신 토큰을 발급하는 서버에서는 만료된 로그인을 자동으로 갱신합니다. 갱신 토큰이 없거나 철회된 경우 F9에서 다시 로그인합니다. Bearer 환경변수로도 연결할 수 있습니다. [MCP 인증 명세](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)

## MCP 도구 명령

채팅에서 `/mcp`를 입력하거나 터미널에서 `freeway mcp manage`를 실행하면 일반 MCP 서버를 저장하고, 연결 상태를 확인하고, 인증을 바꿀 수 있습니다. 브라우저 로그인과 Bearer 환경변수를 지원합니다. 정확히 같은 URL의 인증이 하나 있으면 일반 MCP와 F9에서 함께 사용할 수 있습니다.

`/mcp-ask 질문`은 질문과 일반 MCP 도구의 설명·입력 항목 요약을 선택한 모델에 보냅니다. 승인 화면에서 실제 서버, 도구, JSON 인수를 확인하고 F8로 실행하거나 Esc로 취소합니다. 긴 인수는 끝까지 스크롤한 뒤 승인할 수 있습니다. 도구를 한 번 호출하고 텍스트·구조화 결과를 선택한 모델의 답변에 사용합니다. 이 요청은 첨부 파일을 지원하지 않습니다.

`freeway mcp tools`는 저장한 서버와 F9 연결에서 도구와 입력 스키마를 조회합니다. 조회 결과의 `server` ID로 `freeway mcp call --server ID --args-file arguments.json TOOL`을 실행합니다. 연결 순서를 바꿔도 서버 ID는 유지됩니다. `--args-file -`는 표준 입력에서 JSON 객체를 읽습니다. 일회성 Streamable HTTP 연결에는 `--url URL`을 사용하고, 토큰이 필요하면 `--token-env FREEWAY_MCP_TOKEN`을 함께 지정합니다. 명령 결과는 JSON입니다.

## 연결 계약

[OpenAPI v1 명세](https://github.com/wooinwoo/freeway-cli/blob/main/api/openapi.yaml)에 Bearer 인증, 준비된 무료 모델 조회, SSE 응답, revision을 확인하는 대화 저장이 정의되어 있습니다. [백엔드 인계 문서](https://github.com/wooinwoo/freeway-cli/blob/main/docs/development/client-delivery.md#backend-handoff)에서 연결 완료 기준을 확인할 수 있습니다.
