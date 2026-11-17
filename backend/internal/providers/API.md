# 공급처 API 명세

명세 문서 버전: 0.1.0. 기준일: 2026-11-17. 대상 구현: [client.go](client.go), [bridge.mjs](bridge.mjs), [manage.mjs](manage.mjs). 이 버전은 문서의 버전이며 현재 메시지에 `version` 필드나 버전 협상은 없습니다.

## 1. 제공하는 API와 실행 조건

| 경계 | 현재 제공하는 것 |
| --- | --- |
| 백엔드가 사용하는 API | Go의 `providers.Client` 함수 호출 |
| Go와 Node 사이 | 프로세스 1개당 stdin JSON 요청 1개, stdout JSON Lines 이벤트 |
| 기존 게이트웨이 | Node 브리지가 기존 로컬 관리 API·추론 HTTP API를 사용 |
| 새 Go HTTP 서버 | 이 패키지에서 제공하지 않음. `freewayd`와 루트 `api/openapi.yaml`은 별도 개발 틀 |

```text
Go backend → providers.Client → bridge.mjs → 기존 로컬 POC gateway → 공급처 API/공식 CLI
```

Node.js 22 이상, 이 폴더와 저장소의 `POC/`, 실행 중인 로컬 게이트웨이가 필요합니다. OpenCode·Cline에는 공식 실행 파일과 필요한 로그인이 있어야 합니다. 키·한도 원장은 같은 게이트웨이를 재사용합니다. 설치·실행 순서는 [연동 방법](INTEGRATION.md)을 따릅니다.

## 2. Go 함수 계약

모듈 내부 import 경로는 `github.com/wooinwoo/freeway-cli/backend/internal/providers`입니다. Go의 `internal` 규칙에 따라 `backend/` 모듈 안에서 사용합니다.

```go
func New(config Config) (*Client, error)
func (c *Client) Providers(ctx context.Context) ([]Provider, error)
func (c *Client) Authenticate(ctx context.Context, provider string, options AuthOptions) (AuthResult, error)
func (c *Client) Login(ctx context.Context, provider string, stdin io.Reader, stdout, stderr io.Writer) error
func (c *Client) Models(ctx context.Context, provider string) ([]Model, error)
func (c *Client) Quota(ctx context.Context, provider string) (Quota, error)
func (c *Client) Complete(ctx context.Context, request Request) (Completion, error)
func (c *Client) Stream(ctx context.Context, request Request, emit func(string)) (Completion, error)
```

### 초기화: Config

| Go 필드 | 필수·기본값 | 계약 |
| --- | --- | --- |
| `NodePath string` | 생략 시 PATH에서 `node` 검색 | 절대 경로여야 함. Node 버전·실행 가능 여부는 실행 환경에서 보장 |
| `BridgePath string` | 필수 | 이 폴더의 `bridge.mjs` 절대 경로. 생성 시 존재 여부 확인 |
| `BaseURL string` | 기본 `http://127.0.0.1:4141` | `http`와 `127.0.0.1`·`localhost`·`::1`만 허용. 사용자 정보·쿼리·fragment·`/v1` 등 하위 경로 금지. 루트 `/`는 허용 |

`New`는 네트워크 요청을 하지 않습니다. Go 패키지는 `FREEWAY_URL`이나 `FREEWAY_PROVIDER_BRIDGE`를 자동으로 읽지 않습니다. [실행 예제](examples/basic/main.go)가 해당 환경변수를 읽어 Config에 넣습니다.

### 공급처 목록: Providers

게이트웨이에 접속하지 않고 공급처 정의를 반환합니다. 반환 `Provider`의 필드는 `ID`, `Name`, `Kind`, `AuthURL`, `Env`입니다. 키의 값은 반환하지 않습니다.

| Kind | 공급처 ID |
| --- | --- |
| `openai` | `kilo`, `nvidia`, `zai`, `requesty`, `vikasit`, `openrouter`, `groq`, `gemini_api`, `novita` |
| `opencode` | `opencode` |
| `cli` | `cline` |

`openai`는 전송 형식을 뜻하며 OpenAI 업체 계정이라는 뜻은 아닙니다.

### 인증 구성: Authenticate와 Login

| 입력 | 동작 |
| --- | --- |
| `AuthOptions{}` | Kilo 익명 연결, 기존 API 키 또는 기존 공식 CLI 로그인 재사용 |
| `AuthOptions{Key: key}` | API 키를 stdin으로 전달하고 저장. 기존 무료 계정 확인 초기화 |
| `AuthOptions{Key: key, ConfirmFree: true}` | 새 키의 무료 조건을 호출자가 확인한 경우에만 사용 |
| `AuthOptions{ConfirmFree: true}` | 기존 계정의 무료 확인 기록. 요금제나 잔액을 실제 조회하는 동작은 아님 |

`Key == ""`는 키를 지우지 않고 기존 자격증명을 유지합니다. 키를 보내지 않은 상태의 `ConfirmFree: false`는 기존 확인을 해제하는 명령이 아닙니다. OpenCode·Cline에 API 키를 전달하면 `cli_login_required` 오류입니다.

API 키는 최대 4096자이며 공백만 있는 값·제어문자·줄바꿈을 거절합니다. 주변 공백은 저장 전에 제거합니다. 키의 실제 유효성과 계정의 무료 조건은 별도 확인 대상입니다.

`AuthResult`의 필드는 `ID`, `Status`, `Ready`, `Reason`, `InferenceVerified`, `ModelCount`입니다. 정상 구성 시 `Status == "configured"`, `InferenceVerified == false`입니다. 무료 조건 미확인 등으로 `err == nil`이어도 `Ready == false`일 수 있으므로 두 값을 모두 확인합니다. 인증 구성은 추론 성공 증거가 아닙니다.

`Login`은 `opencode`·`cline`만 받습니다. 공식 인증을 대화형 터미널에서 실행하므로 웹 요청 처리기에 넣지 않습니다. 성공 이후에도 Models·실제 호출을 별도로 확인합니다. 취소 시 해당 로그인 프로세스와 자식을 종료합니다. Login 오류는 `*providers.Error`로 정규화하지 않으며 일반 실행 오류 또는 context 오류입니다.

### 모델·추론 설정: Models

| Model 필드 | 의미 |
| --- | --- |
| `ID string` | 업체를 포함한 경로 ID. 예: `kilo/reflection/beam-free` |
| `UpstreamID string` | 업체가 사용하는 ID. 예: `reflection/beam-free` |
| `Name string` | 표시용 이름. 호출 ID로 사용하지 않음 |
| `Ready bool`, `Reason string` | 현재 로컬 게이트웨이에서 본 준비 상태와 사유 |
| `Reasoning.Parameter string` | 내부 전송 필드: `reasoning`, `reasoning_effort`, `variant`. 미제공이면 Go에서 빈 문자열 |
| `Reasoning.Efforts []string` | 해당 모델에서 노출한 선택값. 비어 있으면 이 패키지에서 추론 깊이 지정 불가 |

활성 공급처의 모델을 동기화한 뒤 무료 필터를 통과한 목록을 반환합니다. 비활성 공급처는 저장된 목록을 반환할 수 있으며 `Models`가 모든 공급처를 활성화하지 않습니다. 초기 연결은 Authenticate로 수행합니다. `Ready`는 요청 성공을 보장하지 않으며 모델 목록·지원값은 바뀔 수 있습니다.

Kilo의 추론 값은 모델별 `reasoning` 또는 `reasoning_effort`로, OpenCode는 세션 `variant`로 전달합니다. Cline ACP는 현재 지원값을 노출하지 않습니다. 지원하지 않는 깊이는 조용히 무시하지 않고 `unsupported_reasoning`으로 거절합니다. 모델의 추론 켜기·끄기나 토큰 예산 전체를 통합한 API는 아직 제공하지 않습니다.

### 한도: Quota

추론 요청이나 사용량 예약 없이 게이트웨이의 관측 상태를 반환합니다.

| Quota 필드 | 타입·단위 | 의미 |
| --- | --- | --- |
| `Provider` | string | 공급처 ID |
| `Source` | string | 현재 `local_gateway` |
| `Authoritative` | bool | 현재 `false`. 업체의 실시간 잔여 할당량 조회가 아님 |
| `ObservedAt` | `time.Time` | 이 결과를 만든 UTC 시각 |
| `Ready`, `Reason` | bool, string | 준비 상태·한도·cooldown 등 현재 사유 |
| `Limits` | `map[string]int` | 로컬 설정: `minute`, `hour`, `day`는 요청 수, `dayTokens`는 토큰 |
| `Usage` | `map[string]Bucket` | `minute`, `hour`, `day`, `total`의 로컬 집계 |
| `LocalRemaining` | `map[string]int` | 로컬 요청 수 상한에서 해당 기간 요청 수를 뺀 값, 최소 0 |
| `LocalTokenRemaining` | `map[string]int` | `day`의 로컬 토큰 상한에서 관측·추정 토큰을 뺀 값, 최소 0 |
| `ProviderRemaining` | `*int` | 현재 `nil`. 업체가 정확한 잔여량을 제공하지 않음 |
| `LastFailure` | `json.RawMessage` | 저장된 마지막 실패 로그 또는 `null`. 정해진 공통 오류 타입 대신 관측 자료 |

`Bucket`은 `Requests`, `Tokens`, `EstimatedTokens` 정수 필드를 가집니다. map에 항목이 없으면 제한을 모르는 것이며 0과 다릅니다. `ProviderRemaining == nil`은 무제한이나 소진을 뜻하지 않습니다. 다른 앱·PC·같은 IP의 사용량은 로컬 집계에 포함되지 않습니다. API 키의 실제 요금제·잔액이나 공식 CLI의 전체 사용량을 조회하는 기능은 아닙니다.

### 호출: Request와 Completion

| Request 필드 | 필수·기본값 | 계약 |
| --- | --- | --- |
| `Provider string` | 필수 | 위 공급처 ID 중 하나 |
| `Model string` | 필수 | 그 공급처의 ID 또는 UpstreamID. 그룹 ID·`free/auto`는 이 Go API에서 받지 않음 |
| `Messages []Message` | 필수 | 1~200개 텍스트 대화. `Message`는 `Role`, `Content` 문자열. 지원 대상으로 삼는 대화 역할은 `system`, `developer`, `user`, `assistant` |
| `MaxTokens int` | 0이면 생략, 브리지 기본 2048 | 지정 시 1~32768. CLI 공급처에는 같은 상한을 강제하지 못함 |
| `ReasoningEffort string` | 빈 문자열이면 업체 기본값 | Models의 Efforts에 있는 값만 허용. 업체 사이의 동일 품질·시간·토큰 예산을 보장하지 않음 |

전체 stdin 요청 크기는 UTF-8 JSON 기준 최대 2 MiB입니다. 현재 Go 메시지 타입에는 도구 호출·파일·이미지·오디오 필드가 없습니다. 대화별 모델을 바꾸려면 다음 Request의 Model을 바꿉니다. 공유 기본 모델을 저장하거나 다른 업체로 자동 전환하지 않습니다.

Complete와 Stream 모두 먼저 최신 모델 목록·무료 상태·준비 상태·추론 설정을 확인합니다. 반환 `Completion`은 `Model`, `Provider`, `Content`, `ReasoningEffort`, `Usage`, `ElapsedMS`입니다. Model은 정규화한 전체 경로 ID입니다. Usage는 업체별 JSON이며 생략될 수 있습니다. 값을 받지 못했을 때 사용량·비용을 0으로 기록하지 않습니다. ReasoningEffort는 요청 설정의 반환값이지 모델의 사고 과정을 측정한 값이 아닙니다.

Stream 콜백은 같은 호출 안에서 순서대로 실행합니다. 콜백은 빠르게 반환해야 하며 호출자 코드에서 블로킹하면 context 취소 후 함수 반환도 지연될 수 있습니다. `emit == nil`이면 콜백 없이 결과를 모으므로 Complete와 같습니다. 콜백으로 일부 텍스트를 받았어도 최종 `err`가 있으면 실패입니다. 실패 시 반환 Completion을 완성된 답변으로 사용하지 않습니다.

## 3. 오류·시간 제한·취소

```go
var providerError *providers.Error
if errors.As(err, &providerError) {
    // Code로 분기하고, RetryAfter > 0이면 해당 초만큼 기다립니다.
}
if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
    // 취소 또는 호출자 제한 시간 만료입니다.
}
```

`providers.Error`의 필드는 `Code string`, `Status int`, `RetryAfter int`입니다. Status는 실제 게이트웨이 HTTP 상태 또는 브리지의 진단용 대응값입니다. 별도 Go HTTP 서버가 응답한 상태가 아닙니다. 관리 명령에 원본 HTTP 상태가 없으면 현재 브리지는 409를 사용하므로 Code를 우선 확인합니다. DOM 타임아웃·취소도 문자열 코드로 정규화하며, 양수 RetryAfter에 소수점이 있으면 초 단위로 올림합니다. 0은 대기 시간 미제공입니다.

| 대표 Code | Status | 처리 |
| --- | --- | --- |
| `unknown_provider` | 404 | 공급처 ID 수정 |
| `model_not_found` | 404 | 해당 업체의 현재 무료 Models에서 다시 선택 |
| `needs_key`, `cli_login_required` | 보통 409, 원본 인증 실패는 401 등 | 초기 키 구성 또는 대화형 로그인 |
| `unsupported_reasoning` | 400 | 모델의 Efforts 중 선택하거나 설정 생략 |
| `invalid_messages`, `invalid_role`, `invalid_parameter` | 400 | 요청 내용·범위 수정 |
| `unsupported_capability` | 400 | 브리지가 지원하지 않는 입력 제거 |
| `no_free_models` | 503 | 사용 가능한 무료 후보 없음. HTTP 라우터가 도구 미지원 경로를 먼저 제외한 경우에도 발생 가능 |
| `provider_not_ready`, `disabled`, `cooldown`, `confirm_free_account` 등 준비 사유 | 보통 503 | AuthResult·Models·Quota의 Reason 확인 |
| `rate_limit_exceeded` | 429 | RetryAfter가 있으면 준수. 유료 모델 자동 전환 금지 |
| `request_timeout`, `cancelled` | 직접 브리지 타임아웃은 504, 취소는 499. 관리 명령 경유 진단값은 다를 수 있음 | Go 호출자 context 오류와 함께 구분해 처리 |
| `unexpected_route` | 502 | 요청한 업체·모델과 다른 결과. 응답을 성공으로 사용하지 않음 |
| `bridge_failed` | 보통 502 | 게이트웨이 실행·Node·브리지 상태 점검 |

이 표는 폐쇄된 enum이 아닙니다. 공급처·게이트웨이 오류 코드가 추가될 수 있습니다. Config 검증·Node 실행·프로토콜 파싱·Login·context 오류는 일반 Go error로 반환할 수 있습니다. 모든 오류를 `*providers.Error`라고 가정하지 않습니다.

Login을 제외한 Go 함수는 호출자 context와 내부 2분 중 먼저 끝나는 제한을 따릅니다. Login은 호출자가 context 제한 시간을 지정합니다. 게이트웨이 자체 제한 시간도 별도로 적용됩니다. 취소는 진행 중 연결·로그인 자식을 중단하지만 이미 발생한 사용량이나 완료된 설정 변경을 되돌리지 않습니다. 스트림 중간 실패의 상태와 RetryAfter도 전달합니다.

## 4. 내부 JSON 프로토콜

다른 언어가 같은 어댑터를 사용할 때의 기계 판독 명세는 [bridge.schema.json](bridge.schema.json)입니다. 이것은 HTTP OpenAPI나 JSON-RPC가 아닙니다. Go API가 바깥에서 사용하고 있는 현재 프로세스 계약입니다.

- 실행: Node에 `bridge.mjs` 절대 경로를 argv로 전달합니다. 키·프롬프트는 argv에 넣지 않습니다.
- stdin: JSON 객체 하나를 쓰고 EOF로 닫습니다. 한 프로세스에서 여러 요청을 받지 않습니다.
- stdout: 한 줄에 JSON 객체 하나. `text` 이벤트 0개 이상 후 `result` 또는 `error` 하나로 종료합니다. `done` 이벤트는 없습니다.
- 성공 시 종료 코드 0, 정규화된 오류 이벤트를 반환하면 1입니다. 강제 종료·실행 실패에는 오류 이벤트가 없을 수 있습니다.
- Go 클라이언트가 읽는 stdout 한 줄은 최대 2 MiB입니다. stderr를 프로토콜 JSON에 합치지 않습니다.

| action | 요청 필드 | result |
| --- | --- | --- |
| `providers` | action | 공급처 객체 배열. Go Provider보다 공개 안내 필드가 더 있을 수 있음 |
| `authenticate` | action, provider, 선택 key·confirmFree·baseURL | 인증 구성 객체 |
| `models` | action, provider, 선택 baseURL | 모델 객체 배열 |
| `quota` | action, provider, 선택 baseURL | 한도 관측 객체 |
| `complete` | action, provider, model, messages, 선택 maxTokens·reasoningEffort·baseURL | 완료 객체. 진행 중 text 이벤트 가능 |

`baseURL` 기본값은 `http://127.0.0.1:4141`입니다. `providers` 이외 요청은 provider를 먼저 검증합니다. provider가 없거나 미등록이면 `unknown_provider` 404이며, 유효한 provider를 전달한 뒤 지원하지 않는 action이면 `unknown_action` 400입니다. JSON 문법 오류 등 정규화하지 못한 실패는 `bridge_failed`로 반환할 수 있습니다. Login은 이 JSON 프로토콜에 없고 Go에서 대화형 관리 명령으로 실행합니다.

호출 예시:

```json
{"action":"complete","provider":"kilo","model":"reflection/beam-free","messages":[{"role":"user","content":"한 문장으로 인사해 주세요."}],"maxTokens":2048,"reasoningEffort":"high","baseURL":"http://127.0.0.1:4141"}
```

성공 이벤트 예시. content와 수치는 설명용이며 실제 호출마다 달라집니다.

```jsonl
{"type":"text","text":"안녕하세요."}
{"type":"result","result":{"ok":true,"model":"kilo/reflection/beam-free","provider":"kilo","content":"안녕하세요.","usage":{"total_tokens":40,"cost":0},"elapsedMs":1200,"reasoningEffort":"high"}}
```

실패 이벤트 예시:

```json
{"type":"error","error":{"code":"unsupported_reasoning","status":400,"retryAfter":0}}
```

wire JSON의 `reason`, `reasoning.parameter`, `reasoningEffort`가 `null`이면 Go의 대응 string은 빈 문자열로 읽습니다. wire에 없는 usage는 Go에서 빈 RawMessage가 됩니다. ProviderRemaining의 `null`은 Go에서 nil 포인터입니다. Go 객체를 다시 JSON으로 marshal한 결과는 브리지 원문과 생략·null 처리에서 다를 수 있습니다.

JSON Schema는 패킷 구조·정적 범위를 검사합니다. 추가 필드를 허용하지만 해당 기능이 구현됐다는 뜻은 아닙니다. 실제 모델 등록·준비 상태·추론 지원값·인증 정보 형식은 런타임에서 추가 검증합니다.

## 5. 범위와 참고

현재 실호출 검증 공급처는 Kilo·OpenCode·Cline입니다. 나머지 API 8곳은 키가 없어 추론 미검증입니다. Windows는 크로스컴파일까지 확인했고 실제 로그인·호출은 미검증입니다. 현재 문서는 이 구현의 계약이며 최종 Go HTTP 서버의 설계를 대신하지 않습니다.

- [코드와 실행 명령](README.md)
- [인증·HTTP·Go 이식 설명](INTEGRATION.md)
- [실행 가능한 Go 예제](examples/basic/main.go)
- [동작 검증](client_test.go), [브리지 검증](bridge.test.mjs)
