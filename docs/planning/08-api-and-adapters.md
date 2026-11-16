# Freeway API와 어댑터 계약

## 계약의 상태

이 문서에서 기존 API와 제안 API를 구분한다. 현재 코드에는 `/v1/models`, `/v1/chat/completions`, 관리 API, MCP 텍스트 위임이 있다. 아래의 경로 조회·정책 설명·작업 API는 새 설계이며 이미 구현됐다고 가정하지 않는다.

## 외부 API 범위

| API | 현재 또는 제안 | 책임 |
| --- | --- | --- |
| `GET /health` | 현재 | 프로세스 상태 확인. 공급처 추론 성공을 뜻하지 않는다 |
| `GET /v1/models` | 현재 | 지원 모델 목록. 새 설계에서는 요청 계약별 검증 상태 반영 |
| `POST /v1/chat/completions` | 현재 | 명시적으로 지원하는 추론 계약 |
| `GET /api/state` | 현재 관리 API | 연결·모델·설정·로그 상태 |
| `GET /api/routes` | 제안 | 경로 종류·기능·비용·권한·상태 조회 |
| `POST /api/routing/explain` | 제안 | 실제 추론 없이 선택 계획 반환 |
| `GET /api/usage` | 제안 | 실제·추정·예약 사용량과 한도 범위 |
| `POST /api/probes` | 제안 | 사용자가 시작한 제한된 실제 경로 시험 |
| `POST /api/tasks` | 후속 제안 | 외부 에이전트 작업 위임 |
| `GET /api/tasks/:id` | 후속 제안 | 작업 상태와 결과 |
| `POST /api/tasks/:id/cancel` | 후속 제안 | 자신이 시작한 작업 취소 |

관리 API는 일반 추론용 토큰으로 설정을 바꾸지 못하도록 별도 권한을 유지한다. 기존 브라우저 세션과 로컬 CLI의 관리 권한 연결 방식은 구현 단계에서 하나의 명확한 계약으로 정한다. API 키를 알고 있다는 이유만으로 다른 공급처 비밀을 읽을 수 있게 하지 않는다.

## Chat Completions 지원 표

| 요소 | 첫 버전 계약 |
| --- | --- |
| 메시지 | 기존 `system`, `developer`, `user`, `assistant` 역할을 보존한다. `developer` 변환이 필요한 경로는 의미 보존을 검증한다. 도구 지원 경로에는 `tool`과 도구 호출 이력 |
| 모델 선택 | exact route, model group, `free/auto` |
| 스트림 | SSE, 정상 종료와 중간 오류 구분 |
| 출력 제한 | 어댑터가 지원하는 한도 안에서 검증 |
| 도구 | 실제 호환성을 검증한 경로에서 function 정의와 호출 객체 전달 |
| 이미지·파일·오디오 | 별도 기능 검증 전 지원하지 않음 |
| 구조화 출력 | 경로별 검증 후 지원 표시 |
| 공급처 전용 옵션 | 명시적인 이름 공간과 검증 없이는 전달하지 않음 |
| 지원하지 않는 역할·옵션 | 400과 구체적인 코드 |

기존 클라이언트가 옵션을 추가한다고 조용히 삭제하지 않는다. 호환성 때문에 무시할 필드를 정한다면 목록과 동작을 고정한다. 공급처별로 결과가 달라지는 옵션은 capability에 반영한다.

## 요청 예시

기존 형태를 유지하는 텍스트 요청이다.

```json
{
  "model": "free/auto",
  "messages": [{"role": "user", "content": "이 함수의 경계 조건을 설명해 줘."}],
  "max_tokens": 1024,
  "stream": true
}
```

정책 확장이 필요하면 `freeway` 이름 공간을 사용하는 형태를 제안한다. 현재 코드가 이 확장을 처리한다고 가정하지 않는다.

```json
{
  "model": "free/auto",
  "messages": [{"role": "user", "content": "요약해 줘."}],
  "freeway": {
    "policy": "preferred-free",
    "allow_model_switch": true,
    "max_attempts": 3
  }
}
```

요청별 설정은 서버의 비용·권한·자원 상한을 느슨하게 만들 수 없다. 예를 들어 서버의 strict-free 기본 정책을 요청 본문에서 유료 허용으로 덮어쓰는 기능은 초기 범위에 없다.

## 경로 상태 예시

```json
{
  "id": "requesty/muse-glimmer-30b",
  "kind": "inference-api",
  "model": "model/meta/muse-glimmer-30b",
  "status": "needs_auth",
  "capabilities": {"text": "verified", "tools": "unknown", "stream": "unknown"},
  "price": {
    "classification": "documented-free",
    "observed_at": "2026-11-16",
    "inference_verified": false
  },
  "quota": {"unit": "requests", "remaining": null, "confidence": "unknown"}
}
```

이 예시는 문서 근거와 실제 호출 근거를 분리하는 방식이다. `documented-free`는 연결 완료나 도구 지원의 의미가 아니다.

## 어댑터 인터페이스 제안

```ts
type RouteKind = 'inference-api' | 'cli-text' | 'agent-task';
type EvidenceState = 'verified' | 'documented' | 'unknown' | 'unsupported';

interface Adapter {
  id: string;
  kind: RouteKind;
  inspectConnection(signal: AbortSignal): Promise<ConnectionState>;
  listModels(signal: AbortSignal): Promise<CatalogSnapshot>;
  getQuota?(signal: AbortSignal): Promise<QuotaSnapshot>;
  validate(request: NormalizedRequest, route: Route): ValidationResult;
  complete(request: NormalizedRequest, context: AttemptContext): AsyncIterable<InferenceEvent>;
}
```

위 타입은 책임을 설명하는 설계 예시다. `agent-task`는 `complete`를 억지로 구현하지 않고 작업 실행 계약을 별도로 가진다. 코드에 빈 메서드와 불필요한 추상 클래스를 먼저 만들 필요는 없다. 실제 두 어댑터의 공통 계약이 확인된 후 추출한다.

`AttemptContext`에는 취소 신호, 요청 ID, 시도 ID, 시간 제한, 해당 연결의 자격증명 참조, 허용 기능이 들어간다. 다른 계정 전체나 서버 관리 키를 어댑터에 전달하지 않는다.

## 정규화 이벤트

| 이벤트 | 필요한 내용 |
| --- | --- |
| started | 요청·시도·실제 모델·경로 식별 |
| text delta | 텍스트 조각과 순서 |
| tool call delta | index, ID, 이름, 인자 조각 |
| usage | 실제 토큰·추정 토큰·비용·비용 확인 수준 |
| completed | finish reason과 최종 메타데이터 |
| failed | 오류 분류, 재시도 가능성, 제한 범위, partial 여부 |

CLI progress 로그를 텍스트 delta로 오인하지 않는다. stdout과 stderr를 섞지 않고, CLI 버전별 JSON 이벤트를 분리해서 읽는다. JSON 파싱 실패는 공급처 텍스트 답변으로 넘기지 않는다.

## 오류 응답

기존 OpenAI 형태의 `error` 객체를 유지하되, 제품 내부 분류를 안정적인 `code`로 표현한다.

```json
{
  "error": {
    "type": "gateway_error",
    "code": "no_eligible_free_route",
    "message": "요청 기능을 지원하는 무료 경로가 없습니다.",
    "request_id": "fwreq_example"
  }
}
```

| 코드 | 의미 | HTTP 제안 |
| --- | --- | ---: |
| invalid_request | 요청 형식 또는 옵션 오류 | 400 |
| unsupported_capability | 지원 가능한 경로가 요청 기능을 충족하지 못함 | 400 |
| invalid_api_key | Freeway API 인증 실패 | 401 |
| connection_auth_required | 연결 재인증 필요 | 503 |
| provider_permission_denied | 공급처 권한 제한 | 503 |
| quota_exhausted | 활성 무료 경로의 확인된 한도 소진 | 429 |
| local_budget_guard | 포함 범위가 불명확한 소비 또는 로컬 안전 상한으로 차단 | 429 |
| no_eligible_free_route | 가격·권한·기능 조건을 만족하는 후보 없음 | 503 |
| upstream_unavailable | 전송 전 또는 시작 전 공급처 장애 | 502 |
| upstream_timeout | 요청 시간 예산 소진 | 504 |
| partial_response_failed | 스트림 커밋 후 실패 | 이미 시작된 SSE의 오류 이벤트 |

개별 공급처 오류를 그대로 사용자에게 노출하면 키·계정·내부 경로가 포함될 수 있다. 원문 오류는 마스킹한 제한 길이의 진단으로만 보관한다.

## 작업 위임 API의 별도 계약

작업 요청은 `prompt`, `backend`, `workspace`, `readOnly`, `deadline`, `context`를 가진다. 결과는 `taskId`, `status`, `answer`, `filesChanged`, `toolActions`, `usage`, `cancelled` 여부를 포함한다. 초기 read-only 경로에서 파일 변경이 감지되면 계약 실패로 취급한다.

`readOnly`는 프롬프트나 사후 파일 비교만으로 구현하지 않는다. 실행 전에 공식 도구 정책·hook 또는 검증된 OS 격리로 쓰기와 임의 명령 실행을 차단해야 한다. Cline은 shell 명령 차단에 `PreToolUse` hook을 사용하도록 안내한다. 해당 기능을 사용하는 경우 파일 변경 도구와 `run_commands`를 실행 전에 거절하고, 검증되지 않은 새 도구도 기본 거절한다. [Cline 명령 실행 제한](https://docs.cline.bot/usage/cli-overview)

어댑터에는 `readOnlyEnforcement`의 지원 여부와 시험 근거를 둔다. 허용 작업 영역 밖 접근과 심볼릭 링크를 통한 탈출을 차단할 수 있는지 확인하고, 사용자 플러그인이나 다른 hook이 보호 설정을 우회하지 않도록 실행 구성을 격리한다. 필요한 차단을 강제할 수 없는 어댑터는 read-only 작업 후보에서 제외한다.

사후 파일 비교는 실행 전 보호의 회귀를 찾는 보조 검증이다. 삭제나 작업 영역 밖 변경이 발생한 뒤 오류를 표시하는 것으로 read-only를 보장하지 않는다. 이런 경로를 사용할 필요가 있다면 read-only가 아닌 별도 권한의 작업으로 사용자가 명시적으로 선택해야 한다.

작업 응답을 `/v1/chat/completions`의 임의 도구 응답인 것처럼 변환하지 않는다. 클라이언트의 tool call loop와 외부 에이전트의 자체 loop는 소유자가 다르다.

## 호환성 유지

현재 Freeway의 공급처별 모델 ID와 그룹 ID는 전환 기간에 유지한다. 새 canonical ID와 기존 ID의 매핑을 제공하며, 모호한 ID는 경고와 함께 명확한 대상을 요구한다. 기존 API 클라이언트가 받는 `model`의 의미를 조용히 바꾸지 않는다.

MCP의 `freeway_delegate`는 현재 텍스트 조언 도구다. 외부 에이전트가 파일을 변경하는 작업을 여기에 추가하면 기존 read-only 설명이 깨진다. 그런 기능은 별도 도구로 추가하고 권한과 부작용을 명시해야 한다.
