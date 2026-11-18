---
title: 로컬 게이트웨이
description: 터미널 작업 공간을 로컬 API v1 서버에 연결합니다.
---

공통 Go 게이트웨이는 구현 예정입니다. 현재 체크아웃에서는 [오프라인 미리보기와 공급처 직접 호출](../)을 각각 실행할 수 있습니다. 호환되는 로컬 API v1 서버가 준비되면 아래 방법으로 연결합니다.

## 연결

루프백 주소에서 서버를 실행하고 클라이언트 키를 발급받습니다. Bash나 Zsh에서는 Freeway를 실행할 터미널에서 주소를 지정하고 키를 입력하세요.

```sh
export FREEWAY_URL=http://127.0.0.1:4141
read -rs FREEWAY_API_KEY
export FREEWAY_API_KEY
freeway
```

PowerShell에서는 현재 프로세스에 `$env:FREEWAY_URL`과 `$env:FREEWAY_API_KEY`를 설정합니다. 클라이언트 키는 Freeway를 로컬 게이트웨이에 인증하는 데 씁니다. 공급처 키는 게이트웨이의 계정 설정에서 구성하세요.

`/model`에서 준비된 모델을 선택하고 질문을 보냅니다. Esc로 응답을 취소할 수 있습니다. `freeway sessions`로 프로젝트 세션을 찾고 `freeway resume SESSION_ID`로 이어갑니다.

## 연결 계약

[OpenAPI v1 명세](https://github.com/wooinwoo/freeway-cli/blob/main/api/openapi.yaml)에 Bearer 인증, 준비된 무료 모델 조회, SSE 응답, revision을 확인하는 대화 저장이 정의되어 있습니다. [백엔드 인계 문서](https://github.com/wooinwoo/freeway-cli/blob/main/docs/development/client-delivery.md#backend-handoff)에서 연결 완료 기준을 확인할 수 있습니다.
