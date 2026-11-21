# API 참고

기준 계약은 [`api/openapi.yaml`](../../api/openapi.yaml)입니다. 인증된 로컬 HTTP 요청으로 모델과 대화를 조회하고 SSE로 응답을 받습니다.

게이트웨이가 `X-Freeway-Features`에 `idempotent-saves`를 표시하면 대화 저장 요청에 `Idempotency-Key`를 사용할 수 있습니다. 응답을 확인하지 못했다면 같은 요청 본문과 키를 다시 사용하세요. 다른 편집은 새 키를 사용하며 수정에는 현재 revision이 필요합니다.
