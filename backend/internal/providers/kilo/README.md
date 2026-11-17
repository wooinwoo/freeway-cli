# Kilo

공급처 ID: `kilo`. 공통 실행 순서와 Go 사용법은 [공급처 README](../README.md)를 따릅니다.

## 인증과 연결

무료 모델은 키 없이 auth kilo로 연결할 수 있습니다. 계정 키를 쓸 때는 공식 대시보드에서 발급한 KILO_API_KEY를 숨김 입력 또는 --from-env로 전달합니다.

```sh
freeway-providers auth kilo
freeway-providers status kilo
freeway-providers models kilo
```

이미 `KILO_API_KEY` 환경변수가 준비되어 있으면 `freeway-providers auth kilo --from-env`로 가져옵니다. 비대화형 입력은 `auth kilo --stdin`을 사용합니다.

## 모델 변경과 실제 요청

Go 백엔드는 공통 [공급처 패키지](../README.md#go에서-사용)에 `Provider: "kilo"`를 지정합니다. 모델별 `Reasoning.Efforts`를 조회하고 `Request.ReasoningEffort`를 설정합니다. CLI는 `--reasoning-effort high`를 지원합니다. 예를 들어 `reflection/beam-free`의 `low/high` 요청을 실제 Go 코드로 확인했습니다. `Quota`·`quota kilo`는 이 클라이언트의 관측이며 업체의 정확한 잔여량은 미제공이면 `null`입니다.

다음 ID는 예시입니다. 현재 목록에 있을 때만 사용할 수 있습니다. `models kilo`는 각 모델의 실제 ID와 실행 가능한 `select`, `probe`, `ask` argv 배열을 함께 출력합니다.

```sh
freeway-providers select kilo nvidia/nemotron-3-ultra-550b-a55b:free
freeway-providers probe kilo --model nvidia/nemotron-3-ultra-550b-a55b:free
freeway-providers ask kilo --model nvidia/nemotron-3-ultra-550b-a55b:free -- "한 문장으로 인사해 주세요."
```

## 백엔드 연결 근거

현재 공식 문서는 기본 주소를 `https://ai-gateway.kilo.ai/api/v1`로 안내합니다. 현재 Go 어댑터는 아래 호환 주소로 모델 조회·익명 SSE 호출을 실검증했습니다. 공식 안내의 기본 주소와 구분하며 URL을 임의로 교체하지 않습니다.

- 요청 기본 주소: `https://api.kilo.ai/api/gateway` (현재 Go 어댑터 설정).
- 인증: `Authorization: Bearer`와 계정 API 키. 이 공급처의 무료 경로는 키를 생략할 수 있습니다.
- 모델 조회: `GET https://api.kilo.ai/api/gateway/models`. Go 무료 필터를 거친 결과만 실행합니다.
- 추론: `POST https://api.kilo.ai/api/gateway/chat/completions`. 공급처의 `upstreamId`를 `model`로 보냅니다. JSON 응답 또는 SSE 응답을 Go 어댑터가 처리합니다.
- 취소: Go HTTP 요청의 context를 취소해 연결·스트림을 중단합니다.
- Go 구현: [Go HTTP 어댑터](../native_http.go), [Go 공급처 정의·무료 필터](../catalog.go).

## 무료 판정과 오류

입력·출력 단가가 모두 0이고 공식 카탈로그가 `isFree: false`로 표시하지 않은 모델만 사용합니다. 토큰 단가 0만으로는 요청·음악 생성 등 다른 요금까지 무료라는 뜻이 아닙니다. 익명 무료 요청은 IP별 제한을 받습니다. API 키와 연결된 BYOK의 별도 과금 조건도 확인해야 합니다.

무료·한도 설명: [공식 안내](https://kilo.ai/docs/gateway/usage-and-billing). 공식 인증·API 안내: [문서](https://kilo.ai/docs/gateway). 확인 기준일: 2026-11-17. 공급처 정책이 바뀔 수 있으므로 실제 실행 결과를 별도로 확인해야 합니다.

## 전체 모델 실제 호출 결과

2026-11-17, Go HTTP 어댑터로 익명 무료 모델 13개를 하나씩 지정했습니다. 12개가 실제 assistant 응답 `FREEWAY_PROVIDER_OK`를 반환했고 보고된 비용은 0이었습니다. Glyph Cluster는 업체 HTTP 503으로 실패했습니다. 자동 라우터 두 개는 별도의 기반 모델 수로 계산하지 않습니다.

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
| `stealth/glyph-cluster` | 업체 HTTP 503 |

수정 전 필터는 17개를 무료로 분류했습니다. `google/lyria-3-pro-preview`, `google/lyria-3-clip-preview`, `recursive/flint-v1.0`, `humansand/ampersand-v0.1`은 실제 호출에서 HTTP 401과 `PAID_MODEL_AUTH_REQUIRED`를 반환했습니다. 네 모델 모두 카탈로그의 `isFree`가 `false`였습니다. 이 명시적 유료 표시를 우선하도록 수정했으며 무료 경로에서 제외합니다. 네 요청은 인증 단계에서 거절됐고 추론 성공이나 비용 0 성공으로 계산하지 않습니다.
