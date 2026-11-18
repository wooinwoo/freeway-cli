# 공급처 Go API 명세

명세 버전: 0.2.0. 이 버전부터 Go가 공급처 API와 공식 CLI에 직접 연결합니다. 기존 Node 브리지용 NodePath·BridgePath·BaseURL과 stdin JSON 프로토콜은 제거했습니다. 백엔드 HTTP 서버의 API 명세는 이 패키지의 범위가 아닙니다.

## 초기화·소유권

```go
func New(Config) (*Client, error)
func (*Client) Close() error

type Config struct {
    HTTPClient     *http.Client
    Credentials    CredentialStore
    WorkDir        string
    OpenCodeBinary string
    ClineBinary    string
}
```

| 설정 | 계약 |
| --- | --- |
| HTTPClient | 생략하면 Go 기본 HTTP 클라이언트. 업체 API·공식 CLI HTTP에 사용 |
| Credentials | 백엔드의 비밀 저장소. 생략하면 이 Client의 메모리만 사용 |
| WorkDir | 공식 CLI 작업 폴더. 생략하면 임시 폴더를 만들고 Close 때 정리 |
| OpenCodeBinary·ClineBinary | 명시한 공식 실행 파일. 생략하면 환경변수·관리 설치·PATH에서 발견 |

New는 공급처 네트워크 요청이나 공식 CLI 시작을 하지 않습니다. Close는 새 원격 작업을 거절하고 진행 중 작업·소유한 CLI를 중단·대기합니다. 콜백에서 Close를 동기 호출하지 않으며 콜백은 빠르게 반환해야 합니다. 기존 사용자의 CLI·게이트웨이 프로세스는 종료하지 않습니다.

백엔드가 라우팅·쿼터 예약·정산·사용량 저장·HTTP 서비스의 수명주기를 소유합니다. 공급처 어댑터는 다른 업체를 선택하거나 공용 원장을 열지 않습니다.

## 인증 저장 계약

```go
type Credential struct {
    Key string
    FreeConfirmed bool
}
type CredentialStore interface {
    Load(context.Context, string) (Credential, error)
    Save(context.Context, string, Credential) error
}
```

Load의 미등록 업체는 영값과 nil입니다. 저장된 Key가 없으면 업체의 환경변수를 읽습니다. Save의 원자성·암호화·접근 제어는 저장소 구현이 담당합니다. Key는 JSON 직렬화에서 제외하므로 Credential 전체를 단순 JSON 저장하는 방식으로 구현하지 않습니다.

저장소가 `CompareAndSwap(context.Context, string, Credential, Credential) error`도 구현하면 Authenticate는 읽은 원본과 현재 저장 값을 비교한 뒤 변경합니다. 두 Credential 인수는 각각 원본과 새 값입니다. 값이 달라졌으면 `credential_conflict`·HTTP 409를 반환하며 오래된 키나 무료 확인을 복원하지 않습니다. 비교와 저장은 하나의 잠금·트랜잭션 안에서 처리해야 합니다. 기본 메모리·파일 저장소는 이 메서드를 제공합니다. Load·Save만 구현한 외부 저장소는 기존 호환 경로를 사용하므로 백엔드가 동시 인증 변경을 직렬화해야 합니다.

NewFileCredentials(dir)는 독립 CLI·예제용 선택형 구현입니다. 전용 디렉터리의 AES-256-GCM 암호문과 별도 개인 키를 사용하고 다른 업체 값을 보존하며 원자적으로 저장합니다. 기존 Node 저장소는 읽거나 변경하지 않습니다. 실제 백엔드는 공통 비밀 저장소를 위 인터페이스로 연결할 수 있습니다.

## 메서드

```go
func (*Client) Providers(context.Context) ([]Provider, error)
func (*Client) Authenticate(context.Context, string, AuthOptions) (AuthResult, error)
func (*Client) Login(context.Context, string, io.Reader, io.Writer, io.Writer) error
func (*Client) Models(context.Context, string) ([]Model, error)
func (*Client) Quota(context.Context, string) (Quota, error)
func (*Client) Complete(context.Context, Request) (Completion, error)
func (*Client) Stream(context.Context, Request, func(string)) (Completion, error)
```

### 공급처 목록

Providers는 네트워크 요청 없이 11개 공급처의 ID·Name·Transport·Protocol·RequiresCLI·AuthURL·Env를 반환합니다. Kind는 기존 호환용 필드입니다.

| Transport | Protocol | 업체 |
| --- | --- | --- |
| http | openai_compatible | kilo, nvidia, zai, requesty, vikasit, openrouter, groq, gemini_api, novita |
| cli | opencode_session | opencode |
| cli | acp | cline |

### 인증·연결

AuthOptions는 Key와 ConfirmFree입니다. 빈 Key는 기존 인증 유지입니다. 새 Key를 저장하면 이전 FreeConfirmed를 해제하고, ConfirmFree가 명시적으로 true일 때만 다시 확인합니다. API 키는 최대 4096바이트이며 공백만 있는 값과 제어문자를 거절합니다.

AuthResult는 ID·Status·Ready·Reason·InferenceVerified·ModelCount입니다. err가 없어도 Ready가 false일 수 있습니다. InferenceVerified는 인증 구성에서 false이며 실제 호출로 별도 확인합니다. 무료 계정 확인은 업체 요금제·잔액 조회를 대신하지 않습니다.

Login은 opencode·cline만 받습니다. Go에서 공식 CLI의 대화형 인증 명령을 직접 실행합니다. 사용자 터미널에서 사용하고 웹 요청 처리기에서 호출하지 않습니다. 공식 CLI가 토큰 저장·갱신을 담당합니다.

### 모델·추론 깊이

Models는 현재 무료 목록을 갱신합니다. Model은 ID·Provider·UpstreamID·Name·Free·Ready·Reason·Context·DiscoveredAt·Reasoning을 가집니다. ID는 업체를 포함한 경로이며 UpstreamID는 업체에 전송할 ID입니다.

Reasoning.Parameter는 reasoning·reasoning_effort·variant 중 해당 프로토콜의 필드입니다. Efforts는 허용값 목록입니다. Cline은 추론 깊이를 지원하지 않으므로 목록이 비어 있습니다.

카탈로그에서 명시적으로 유료인 모델·폐기·비활성 모델은 제외합니다. OpenCode는 자체 opencode 풀의 입출력 단가 0 모델만, Cline은 공식 free 배열만 사용합니다. 계정 확인이 필요한 HTTP 업체는 확인 전 모델·추론 호출을 거절합니다.

### 호출

```go
type Request struct {
    Provider        string
    Model           string
    Messages        []Message
    MaxTokens       int
    ReasoningEffort string
    Temperature     *float64
}
```

Provider·Model·Messages는 필수입니다. Model은 같은 업체의 ID 또는 UpstreamID입니다. free/auto·모델 그룹·대체 업체 선택은 이 패키지에서 처리하지 않습니다.

Messages는 system·developer·user·assistant 역할의 텍스트 1~200개입니다. 전체 요청은 UTF-8 JSON 기준 최대 2 MiB입니다. MaxTokens는 생략 시 2048, 명시하면 1~32768입니다. 공식 CLI에는 동일한 출력 상한을 강제하지 못합니다. Temperature는 명시하면 0~2이며 CLI가 동일한 의미로 적용한다고 보장하지 않습니다.

각 호출에서 최신 무료 모델·추론 지원값을 확인하고 정확히 한 경로만 사용합니다. backend가 대화별 Request.Model·ReasoningEffort를 정합니다. 지원하지 않는 추론 깊이는 조용히 무시하지 않습니다.

Completion은 Provider·Model·Content·ReasoningEffort·Usage·ElapsedMS입니다. Usage는 업체별 원문 JSON이며 없으면 nil입니다. 미제공 값을 비용 0으로 기록하지 않습니다.

Stream의 텍스트 콜백은 같은 요청 내에서 순서대로 호출됩니다. 일부 텍스트 후 오류가 반환될 수 있습니다. 최종 err가 있으면 결과를 완성된 답변으로 사용하지 않습니다. Complete는 콜백 없이 전체 텍스트를 모읍니다.

### 한도 관측

Quota는 추론 요청을 보내지 않고 현재 클라이언트의 구성 상태와 마지막 정규화된 공급처 실패를 반환합니다.

| 필드 | 현재 의미 |
| --- | --- |
| Source | provider_adapter |
| Authoritative | false. 업체 잔여량 API를 제공하는 것은 아님 |
| ObservedAt | 조회한 UTC 시각 |
| Ready·Reason | 이 클라이언트의 연결 구성 상태 |
| ProviderRemaining | nil. 실제 업체 잔여량 미제공 |
| LastFailure | 같은 Client에서 관측한 code·status·retryAfter·observedAt 또는 null |
| Limits·Usage·LocalRemaining·LocalTokenRemaining | nil. 공통 로컬 원장의 책임을 가져오지 않음 |

미제공은 0·무제한·한도 소진 중 어느 것도 뜻하지 않습니다. backend가 자신의 공통 원장과 위 관측을 조합해야 합니다. Quota 자체가 재시도나 cooldown을 강제하지 않습니다.

## 오류·취소

공급처 실패는 가능한 경우 Error{Code, Status, RetryAfter}로 정규화합니다. RetryAfter는 초 단위 정수이며 미제공은 0입니다. 문자열 원본 응답·stderr·API 키를 오류에 포함하지 않습니다. 로컬 실행·설정 오류는 일반 Go error일 수 있습니다.

| Code | 대표 상태 | 처리 |
| --- | --- | --- |
| unknown_provider | 404 | 업체 ID 수정 |
| needs_key·cli_login_required | 409 또는 인증 실패 401 | 키 구성·공식 로그인 |
| confirm_free_account | 409 | 계정 무료 조건 확인 |
| model_not_found·not_free_model | 404 또는 400 | 현재 무료 목록에서 선택 |
| unsupported_reasoning·unsupported_capability | 400 | 지원하는 값·텍스트 입력만 사용 |
| rate_limit_exceeded | 429 | RetryAfter가 있으면 backend 정책에서 준수 |
| invalid_stream·incomplete_stream·empty_completion | 502 | 부분 답변을 성공으로 확정하지 않음 |
| credential_store_failed | 500 | 호출자가 제공한 비밀 저장소 점검 |

Login은 호출자 context의 제한 시간을 따릅니다. 다른 원격 메서드는 호출자 context와 내부 2분 중 먼저 끝나는 제한을 따릅니다. context.Canceled·DeadlineExceeded는 errors.Is로 확인합니다. 취소는 소유한 요청·세션·프로세스를 중단하며 이미 발생한 업체 사용량을 되돌리지 않습니다.

실행 방법은 [Go 예제](examples/README.md), 구현의 최종 필드 기준은 [types.go](types.go)입니다.
