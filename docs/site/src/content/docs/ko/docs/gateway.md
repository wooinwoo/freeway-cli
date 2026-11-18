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

모델 공급처 인증은 `freeway providers auth 공급처ID`로 설정합니다. 자동 게이트웨이로 연결한 채팅에서는 모델 메뉴의 **OpenCode…**를 선택해 설치된 OpenCode CLI를 사용할 수 있습니다. OpenCode·Cline·NVIDIA는 공급처 도구로도 직접 호출할 수 있습니다. Auto는 사용 가능한 HTTP 공급처 중에서 선택합니다.

`/model`에서 준비된 모델을 선택하고 질문을 보냅니다. Esc로 응답을 취소할 수 있습니다. `freeway sessions`로 프로젝트 세션을 찾고 `freeway resume SESSION_ID`로 이어갑니다.

## 연결 계약

[OpenAPI v1 명세](https://github.com/wooinwoo/freeway-cli/blob/main/api/openapi.yaml)에 Bearer 인증, 준비된 무료 모델 조회, SSE 응답, revision을 확인하는 대화 저장이 정의되어 있습니다. [백엔드 인계 문서](https://github.com/wooinwoo/freeway-cli/blob/main/docs/development/client-delivery.md#backend-handoff)에서 연결 완료 기준을 확인할 수 있습니다.
