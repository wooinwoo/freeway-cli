# Kilo

공급처 ID: `kilo`. 인증·연결·모델 시험용 인계 자료입니다. Go 어댑터 구현 완료를 뜻하지 않습니다. 공통 실행 조건은 [상위 안내](../README.md)를 따릅니다.

## 인증과 연결

무료 모델은 키 없이 auth kilo로 연결할 수 있습니다. 계정 키를 쓸 때는 공식 대시보드에서 발급한 KILO_API_KEY를 숨김 입력 또는 --from-env로 전달합니다.

저장소 루트에서 실행합니다. 키는 화면에 표시하지 않는 터미널 입력으로 받으며 코드나 명령행 인수로 전달하지 않습니다.

```sh
node backend/internal/providers/manage.mjs auth kilo
node backend/internal/providers/manage.mjs status kilo
node backend/internal/providers/manage.mjs models kilo
```

이미 `KILO_API_KEY` 환경변수가 준비되어 있으면 `node backend/internal/providers/manage.mjs auth kilo --from-env`로 가져옵니다. 비대화형 입력은 `auth kilo --stdin`을 사용합니다.

## 모델 변경과 실제 요청

다음 ID는 예시입니다. 현재 목록에 있을 때만 사용할 수 있습니다. `models kilo`는 각 모델의 실제 ID와 실행 가능한 `select`, `probe`, `ask` argv 배열을 함께 출력합니다.

```sh
node backend/internal/providers/manage.mjs select kilo nvidia/nemotron-3-ultra-550b-a55b:free
node backend/internal/providers/manage.mjs probe kilo --model nvidia/nemotron-3-ultra-550b-a55b:free
node backend/internal/providers/manage.mjs ask kilo --model nvidia/nemotron-3-ultra-550b-a55b:free -- "한 문장으로 인사해 주세요."
```

`select`는 POC의 기본 모델을 저장합니다. `--model`은 한 요청에서만 모델을 바꿉니다. 업체를 지정한 시험은 다른 업체로 자동 전환하지 않습니다. `probe`는 실제 assistant 응답이 확인 문자열과 일치해야 통과합니다.

## 백엔드 연결 근거

현재 공식 문서는 기본 주소를 `https://ai-gateway.kilo.ai/api/v1`로 안내합니다. 아래는 이번 실계정 시험에서 동작한 기존 POC 주소이며, Go 이식 시 공식 주소의 모델 조회와 요청 계약을 재검증합니다.

- 요청 기본 주소: `https://api.kilo.ai/api/gateway` (현재 POC 설정).
- 인증: `Authorization: Bearer`와 계정 API 키. 이 공급처의 무료 경로는 키를 생략할 수 있습니다.
- 모델 조회: `GET https://api.kilo.ai/api/gateway/models`. POC의 무료 필터를 거친 결과만 실행합니다.
- 추론: `POST https://api.kilo.ai/api/gateway/chat/completions`. 공급처의 `upstreamId`를 `model`로 보냅니다. JSON 응답 또는 SSE 응답을 기존 어댑터가 처리합니다.
- 취소: HTTP 요청의 AbortSignal을 중단합니다. Go 이식 시 HTTP 요청 context 취소를 연결해야 합니다.
- 기존 구현: [공통 API 어댑터](../../../../POC/src/adapters.mjs), [공급처 정의·무료 필터](../../../../POC/src/catalog.mjs).

## 무료 판정과 오류

입력·출력 단가가 모두 0이고 공식 카탈로그가 `isFree: false`로 표시하지 않은 모델만 사용합니다. 토큰 단가 0만으로는 요청·음악 생성 등 다른 요금까지 무료라는 뜻이 아닙니다. 익명 무료 요청은 IP별 제한을 받습니다. API 키와 연결된 BYOK의 별도 과금 조건도 확인해야 합니다.

무료·한도 설명: [공식 안내](https://kilo.ai/docs/gateway/usage-and-billing). 공식 인증·API 안내: [문서](https://kilo.ai/docs/gateway). 확인 기준일: 2026-11-17. 공급처 정책이 바뀔 수 있으므로 실제 실행 결과를 별도로 확인해야 합니다.

인증 실패, 권한 거부, 무료 한도 소진, 프로모션 종료를 성공으로 처리하지 않습니다. HTTP 429와 CLI의 무료 할당 소진은 대기·다른 경로 선택 사유이며 유료 전환 근거가 아닙니다. CLI 출력 제한은 공식 클라이언트가 결정하므로 Freeway의 `max_tokens`만으로 강제된다고 가정하지 않습니다.

## 전체 모델 실제 호출 결과

2026-11-17, 기존 공통 HTTP 어댑터를 통해 익명으로 모델을 하나씩 지정했습니다. 다음 13개 모두 실제 assistant 응답이 `FREEWAY_PROVIDER_OK`와 일치했고 공급처가 보고한 `usage.cost`는 0이었습니다. 자동 라우터 두 개는 별도의 기반 모델 수로 계산하지 않습니다.

| upstreamId | 결과 |
| --- | --- |
| `kilo-auto/free` | 성공 |
| `baidu/ernie-5.5-flash-preview:free` | 성공 |
| `liquid/lfm-3-1.7b:free` | 성공 |
| `particle-ai/particle-1:free` | 성공 |
| `nvidia/nemotron-3.5-super-vl:free` | 성공 |
| `dots-studio/dots-3-note-preview:free` | 성공 |
| `nvidia/nemotron-3.5-lightning:free` | 성공 |
| `cohere/north-mini-code:free` | 성공 |
| `nvidia/nemotron-3-ultra-550b-a55b:free` | 성공 |
| `nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free` | 성공 |
| `openrouter/free` | 성공 |
| `reflection/beam-free` | 성공 |
| `stealth/glyph-cluster` | 성공 |

수정 전 필터는 17개를 무료로 분류했습니다. `google/lyria-3-pro-preview`, `google/lyria-3-clip-preview`, `recursive/flint-v1.0`, `humansand/ampersand-v0.1`은 실제 호출에서 HTTP 401과 `PAID_MODEL_AUTH_REQUIRED`를 반환했습니다. 네 모델 모두 카탈로그의 `isFree`가 `false`였습니다. 이 명시적 유료 표시를 우선하도록 수정했으며 무료 경로에서 제외합니다. 네 요청은 인증 단계에서 거절됐고 추론 성공이나 비용 0 성공으로 계산하지 않습니다.
