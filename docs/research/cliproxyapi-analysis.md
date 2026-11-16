# CLIProxyAPI 구조 분석

분석 대상은 `cliproxyapi`의 커밋 `14144df573c77e8e0e1eeaa450d3e63a068372df`이다. `go.mod:1`의 모듈은 `github.com/router-for-me/CLIProxyAPI/v8`이고, `go.mod:3`은 Go `1.26.10`을 지정한다. 이 문서는 README 소개를 요약하지 않고, 서버 시작점과 실제 요청 실행 경로를 따라 소스를 분석한 결과다. 아래 `경로:라인`은 이 저장소를 기준으로 한다.

## 1. 먼저 알아야 할 결론

CLIProxyAPI는 Freeway와 상당 부분 겹치는 설치형 게이트웨이다. 개인 PC에서 OpenAI·Claude·Gemini 호환 API를 열고, 여러 공급자의 API 키나 OAuth 자격증명을 연결하며, 계정 선택·순환·재시도·cooldown·사용량 기록을 처리한다. 따라서 **“중앙 서버 대신 사용자 PC가 게이트웨이가 된다”는 차이만으로는 CLIProxyAPI와 구분되지 않는다.**

다만 아래 세 가지는 구분해야 한다.

1. **OAuth 자격증명으로 클라우드 API를 호출하는 것**과 **공식 코딩 CLI 프로세스를 실행하는 것**은 다르다. 여기서 추적한 Codex·Claude의 기본 추론 경로는 전자다.
2. **같은 모델을 제공하는 다른 계정으로 요청을 재시도하는 것**과 **다른 코딩 도구에 작업·파일·도구 실행 상태를 넘겨 이어서 수행하는 것**은 다르다. auth selector와 session affinity는 후자를 구현한 증거가 아니다.
3. **quota를 관측하고 초과 계정을 쉬게 하는 것**과 **모든 업체의 무료 잔량·회복시각·가격을 종합해 최적의 0원 경로를 고르는 것**은 다르다. 내장 선택 경로는 우선순위·round-robin·가중치·fill-first와 오류 기반 cooldown을 중심으로 구성돼 있다.

근거는 `sdk/cliproxy/auth/selector.go:1074`, `sdk/cliproxy/auth/conductor_execution.go:655`, `internal/runtime/executor/codex_executor_execute.go:125`, `internal/runtime/executor/claude_executor_execute.go:20`이다.

## 2. 전체 구조와 실제 폴더 구조

핵심 책임은 다음처럼 나뉜다. 전체 디렉터리 중 요청 실행과 운영에 직접 관련된 부분을 표시했다.

```text
cliproxyapi/
├── cmd/server/                 실행 인자·설정·저장소·로그인·서버 시작
├── internal/
│   ├── api/                    Gin 서버·라우트·middleware·관리 API
│   │   └── handlers/management/ 설정·자격증명·OAuth·관측·plugin 관리
│   ├── auth/                   업체별 OAuth 교환·갱신·토큰 저장 구현
│   ├── client/                 클라이언트 호환 기능과 공급자별 보조 기능
│   ├── config/                 실행 설정 및 직렬화
│   ├── registry/               모델 카탈로그·모델별 provider 등록
│   ├── runtime/executor/       업체별 실제 요청 생성·HTTP/WS 실행
│   │   └── helps/              실행·quota·usage·payload 공통 보조 처리
│   ├── translator/             공급자 protocol 간 변환 구현
│   ├── thinking/               reasoning/thinking 정규화와 공급자별 적용
│   ├── watcher/                설정·auth 파일 변경 감지와 hot reload
│   ├── store/                  파일 외 저장소 구현
│   ├── pluginhost/             plugin capability 실행과 연결
│   ├── pluginstore/            plugin 배포물 조회·설치
│   ├── home/                   CLIProxyAPIHome 원격 제어·dispatch 연동
│   ├── homeplugins/            Home과 plugin 구성 동기화
│   ├── cache/                  서명·reasoning replay 등 캐시
│   ├── redisqueue/             사용량 출력 등의 큐 연동
│   ├── managementasset/        관리 화면 asset·설정 snapshot
│   ├── tui/                    Bubbletea 관리용 터미널 UI
│   ├── wsrelay/                WebSocket relay session
│   └── wstunnel/               WebSocket tunnel
├── sdk/
│   ├── access/                 게이트웨이 사용자 인증
│   ├── api/handlers/           OpenAI·Claude·Gemini 호환 handler
│   ├── auth/                   로그인 추상화·token store
│   ├── cliproxy/               builder·service·실행 요소 연결
│   │   ├── auth/              auth manager·scheduler·selector·cooldown
│   │   └── usage/             사용량 record 및 배포
│   ├── translator/             변환 registry·pipeline·format 추상화
│   ├── pluginapi/              plugin 계약
│   └── pluginabi/              plugin ABI 계약
├── examples/                   SDK·plugin 사용 예제
└── test/                       여러 모듈을 잇는 테스트
```

중요한 분리는 **API 진입, 자격증명 선택, 공급자 실행, protocol 변환, 영속화**다. 공급자를 추가할 때 selector 자체에 공급자 HTTP 코드를 넣는 구조가 아니다. `sdk/cliproxy/builder.go:245`의 `Build()`가 구성 요소를 조립하고, `sdk/cliproxy/service_executors.go:336`의 `registerExecutorForAuth()`가 auth 종류에 맞는 executor를 등록한다.

실행 흐름은 다음과 같다.

```text
사용자 도구의 호환 API 요청
  → Gin 라우트와 downstream API key 인증
  → protocol별 handler
  → 모델 router 및 모델 registry 조회
  → auth manager의 가용 자격증명 선택
  → alias·모델 pool·session affinity 적용
  → OAuth 갱신 등 실행 전 자격증명 준비
  → provider executor의 요청 변환과 HTTP/WebSocket 호출
  → 응답·SSE protocol 역변환
  → 결과·quota·사용량 기록
  → 사용자 도구에 응답
```

## 3. 서버는 어떻게 시작되는가

`cmd/server/main.go:678`은 로그인 명령과 일반 서버 실행을 구분한다. 내장 명령에는 Codex, Codex device flow, Claude, Antigravity, Kimi 계열, xAI, Devin, Meta 등이 들어간다. 내장 Gemini CLI 로그인 명령은 이 분기에 없다.

`cmd/server/main.go:691`부터 공통 token store를 하나 등록한다. Postgres·object store·Git을 선택할 수 있고, 기본 경로는 `sdkAuth.NewFileTokenStore()`다. 같은 token store를 로그인과 실행 서비스가 공유하도록 만드는 구성이다.

`sdk/cliproxy/builder.go:245`의 `Build()`는 설정을 검증한 후 token provider, API key provider, watcher factory, 로그인 auth manager, downstream access manager를 준비한다. `builder.go:311`에서 token store와 routing selector를 받아 core auth manager를 생성한다. 여기에 transport, 설정, OAuth 모델 alias, plugin scheduler, result policy를 연결한다.

`sdk/cliproxy/service_lifecycle.go:34`의 `Service.Run()`이 실제 수명주기를 담당한다. 일반 실행에서는 auth store를 읽고, 설정에 들어 있는 API key auth를 등록하고, 선택적으로 cooldown 상태를 복구한다. 이후 executor를 등록하고 `service_lifecycle.go:105`에서 자동 OAuth refresh를 시작한다. `service_lifecycle.go:139`에서 API 서버를 생성하고, `service_lifecycle.go:211` 이후 설정·auth watcher를 시작한다.

여기에는 로컬 실행 모드와 Home 모드가 함께 존재한다. Home 모드는 자격증명 선택·설정·사용량 전달을 원격 제어 계층에 맡길 수 있다. 따라서 제품 전체를 “항상 모든 선택과 자격증명이 PC 안에만 존재하는 프로그램”으로 단정하면 안 된다. 로컬 개인 게이트웨이는 지원하는 배치 방식 중 하나다.

`internal/api/server.go:298`의 실제 listen 주소는 `cfg.Host`와 `cfg.Port`로 정해진다. localhost 전용으로 하드코딩돼 있지 않다.

## 4. HTTP 요청을 실제로 따라가면

### 4.1 진입 API와 downstream 인증

`internal/api/server_routes.go:52`의 `setupRoutes()`가 API를 등록한다. `/v1/chat/completions`, `/v1/completions`, `/v1/messages`, `/v1/responses`, `/v1/responses/compact`와 이미지·음성·영상 관련 경로가 존재한다. Gemini 호환 `/v1beta` 경로도 별도로 등록한다.

`server_routes.go:129` 부근에는 `/backend-api/codex` 별칭이 있다. 이것은 Codex CLI 같은 클라이언트가 이 게이트웨이를 호출할 수 있도록 제공하는 **진입 경로**다. 서버가 내부에서 Codex CLI를 실행한다는 의미는 아니다.

`internal/api/server_middleware.go:486`의 `AuthMiddleware()`는 downstream access manager를 호출한다. 인증 성공 시 API key principal과 access provider를 request context에 넣어 사용량 계층에 전달한다. 게이트웨이에 접속하는 사용자의 API key와, 게이트웨이가 공급자에게 제시하는 OAuth/API key는 분리돼 있다.

### 4.2 handler와 모델·provider 결정

`sdk/api/handlers/openai/openai_handlers.go:118`의 `ChatCompletions()`가 streaming 여부에 따라 일반 응답 또는 streaming 응답으로 분기한다. 일반 요청은 `openai_handlers.go:445`의 `handleNonStreamingResponse()`를 거쳐 공통 handler로 들어간다.

`sdk/api/handlers/handlers_execution.go:47`의 `executeWithAuthManagerFormats()`는 먼저 model router를 적용한다. source protocol과 response protocol, 원래 요청 body, header, query, 모델명, session metadata, interceptor 정보를 유지한 채 `Request`와 `Options`를 구성한다. 마지막에 `AuthManager.Execute()`를 호출한다.

`sdk/api/handlers/handlers_routing.go:128`의 `providersForExecution()`는 강제 provider 지정, model router의 결정, 일반 모델 조회를 구분한다. `handlers_routing.go:362`의 `applyModelRouter()`는 plugin에 요청 body·header·metadata를 넘겨 provider나 executor를 바꾸는 확장점이다.

일반 provider 조회는 `internal/util/provider.go:46`의 `GetProviderName()`을 사용한다. 이 함수는 모델 registry에서 해당 모델을 제공하는 provider 목록을 얻는다. `internal/registry/model_registry.go:1900`의 `GetModelProviders()`가 실제 registry 조회 지점이다.

`sdk/cliproxy/service_models.go:32`는 auth의 provider·auth 종류·설정·제외 모델을 반영해 모델을 등록한다. 이미 제거되거나 교체된 auth의 늦은 등록 작업이 새 상태를 덮어쓰지 않도록 publication fence를 잡는다. 모델 등록에도 동시성 문제가 존재한다는 점을 보여 준다.

모델은 이름만 관리하지 않는다. `internal/registry/model_registry.go:40`의 `ModelInfo`에는 context/output 한도, 입출력 modality, thinking, native capability 등이 들어간다. Freeway가 도구 호출·이미지 입력·reasoning까지 지원하려면 이 능력 정보가 선택과 protocol 변환에 함께 반영돼야 한다.

### 4.3 선택과 실행의 경계

`sdk/cliproxy/auth/conductor_execution.go:202`의 `Manager.Execute()`는 provider를 정규화하고 retry 설정을 읽는다. 일반 로컬 실행에서는 `executeMixedOnce()`를 실행하고, 실패하면 설정된 추가 retry round와 cooldown 대기 조건을 검토한다. Home 모드는 별도의 dispatch 경로로 빠진다.

`conductor_execution.go:655`의 `executeMixedOnce()`는 한 round 안에서 여러 자격증명을 시도한다. 이미 시도한 auth와 실제 시도한 auth를 추적하고, credential retry 상한을 적용한다. 선택 후 해당 auth의 upstream 모델 후보를 해석하고, auth 준비를 거쳐 executor를 호출한다.

`sdk/cliproxy/auth/conductor_selection.go:2988`의 `pickNextMixed()`는 Home·plugin·내장 scheduler를 나눈다. 기본 scheduler 경로에서는 모델을 제공하며 executor가 등록된 provider만 선택 후보가 된다. 실패했다고 무조건 설치된 모든 업체와 모델을 순회하는 구조가 아니다. 원하는 교차 업체 대체를 하려면 같은 요청 모델이 해당 provider에 등록되거나, alias·model router·plugin 정책이 대상을 연결해야 한다.

## 5. 공식 CLI 실행과 OAuth HTTP 실행은 다르다

### 5.1 로그인은 자체 OAuth 흐름이다

`sdk/auth/codex.go:38`의 `CodexAuthenticator.Login()`은 PKCE와 state를 생성하고 로컬 callback 서버를 연다. 브라우저 로그인 후 state를 비교하고, `sdk/auth/codex.go:192`에서 `ExchangeCodeForTokens()`를 호출한다. device flow도 별도 분기로 제공한다.

`internal/auth/codex/openai_auth.go:28`의 token endpoint는 `https://auth.openai.com/oauth/token`이다. `openai_auth.go:128`은 authorization code 교환용 HTTP POST를 만들고, `openai_auth.go:136`에서 직접 전송한다.

Claude도 `sdk/auth/claude.go:38`에서 PKCE·state·callback·브라우저 흐름을 만든다. `internal/auth/claude/anthropic_auth.go:27`의 token endpoint를 사용하며, `anthropic_auth.go:408`에서 교환 요청을 생성하고 직접 HTTP로 전송한다.

즉 이 경로에서 실행하는 외부 프로그램은 로그인 URL을 여는 브라우저일 수 있다. 추론을 위해 공식 `codex`나 `claude` 바이너리를 매번 실행하는 흐름과는 구분된다.

### 5.2 추론은 공급자 backend를 직접 호출한다

| 내장 실행 경로 | 소스에서 확인되는 동작 | 근거 |
|---|---|---|
| Codex | 자격증명 조회 후 기본 `https://chatgpt.com/backend-api/codex`의 `/responses` 호출 | `internal/runtime/executor/codex_executor_execute.go:125`, `:130`, `:197` |
| Claude | 자격증명 조회 후 기본 `https://api.anthropic.com/v1/messages?beta=true`로 HTTP 요청 생성 | `internal/runtime/executor/claude_executor_execute.go:31`, `:35`, `:276` |
| Gemini | 모델별 `/v1beta/models/<model>:generateContent`로 HTTP 요청, streaming은 `streamGenerateContent` | `internal/runtime/executor/gemini_executor.go:193`, `:233`, `:358`, `:399` |
| Codex 자동 transport | downstream WebSocket과 auth 설정에 따라 WS/HTTP를 선택한다. upstream WS가 필수인 요청은 조건 불일치 시 오류를 반환한다. | `internal/runtime/executor/codex_websockets_executor.go:77`, `:98` |

Codex의 `TranslateNonStream()`은 `codex_executor_execute.go:329`, Gemini의 응답 변환은 `gemini_executor.go:282`에 있다. 클라이언트가 말하는 protocol과 실제 공급자 protocol이 다를 수 있으므로 요청과 응답 양쪽을 변환한다. `sdk/translator/registry.go:114`, `:264`는 request envelope와 streaming 변환의 공통 registry 진입점이다.

현재 내장 Gemini executor를 “Gemini CLI의 무료 로그인 경로”로 소개해서는 안 된다. 시작점의 내장 로그인 분기와 Gemini HTTP executor를 연결해 읽으면 그런 실행 경로가 나오지 않는다. 확장 plugin이 다른 auth나 executor를 제공하는 경우는 별도 코드 범위다. `sdk/cliproxy/service_executors.go:336` 이후 native/plugin executor 등록 분기가 이 경계를 만든다.

### 5.3 session affinity는 코딩 작업 이전 기능이 아니다

`sdk/cliproxy/auth/selector.go:1625`의 `SessionAffinitySelector.Pick()`은 명시적 session identity를 먼저 읽고, 없으면 보조 session 추론을 사용한다. 선택한 auth에 session을 묶고, 요청에 canonical session metadata를 전달한다. 캐시 키에는 provider·session·model namespace가 들어간다.

이것은 cache·대화 연속성·업체의 계정별 상태를 유지하는 데 유용하다. 그러나 “OpenCode가 하던 작업을 Cline 프로세스에 넘겨 같은 workspace, 승인 상태, 미완료 도구 실행, 세션 파일까지 복구한다”는 기능과 같지 않다. 여기서 분석한 API 실행 계층은 클라이언트가 보낸 대화와 요청을 처리하며, 코딩 에이전트 자체의 전체 수명주기를 관리하지 않는다.

## 6. auth selector·quota·fallback은 어떻게 동작하는가

### 6.1 우선순위와 계정 선택

`sdk/cliproxy/auth/selector.go:948`의 가용 후보 조회는 사용 불가·quota·cooldown 상태를 반영한다. `selector.go:1045`의 `highestPriorityAuths()`는 가용 후보 중 가장 높은 priority tier만 선택한다. 높은 tier가 모두 막히면 더 낮은 가용 tier가 후보가 될 수 있다.

| selector | 실제 선택 방식 | 소스 |
|---|---|---|
| Round-robin | provider/model별 마지막으로 선택한 ID 다음부터 순환한다. 일시적인 후보 제외가 있어도 위치를 이어 간다. | `sdk/cliproxy/auth/selector.go:1074` |
| Weighted round-robin | 양수 weight를 가진 후보에 smooth weighted 선택을 적용한다. | `sdk/cliproxy/auth/selector.go:1133` |
| Fill-first | 가용 후보 중 정렬상 첫 auth를 선택한다. | `sdk/cliproxy/auth/selector.go:1234` |
| Session affinity | 기존 session binding을 우선하고, 필요하면 fallback selector를 사용한다. | `sdk/cliproxy/auth/selector.go:1625` |

selector 조합은 `sdk/cliproxy/service_config.go:106`의 `newRoutingSelector()`에서 만든다. Freeway에서 “A업체 먼저, 한도 소진 시 B업체”만 구현한다면 이 범위와 기능 중복이 크다.

### 6.2 auth별·모델별 실패 상태

`sdk/cliproxy/auth/conductor_cooldown.go:1492`의 `MarkResult()`가 실행 결과를 가용성 상태로 바꾼다. 오래된 실행 결과가 새로 교체된 credential 상태를 잘못 덮어쓰지 않도록 credential version·registration epoch 등을 검토한다.

`conductor_cooldown.go:1710`은 일시적인 모델 capacity 429와 credential quota 초과를 구분한다. 전자는 해당 모델만 제한하고, 계정의 quota가 모두 소진됐다고 처리하지 않는다. `conductor_cooldown.go:1739`는 401·402·403을, `:1755`는 일반 quota 429를 처리한다. 429는 공급자의 `RetryAfter` 힌트가 있으면 사용하고, 없으면 quota backoff 상태로 다음 회복시각을 계산한다.

Freeway가 참고할 핵심은 “429면 다음 키”라는 조건문보다 **실패의 범위**다. 특정 모델의 일시적인 포화, 계정 전체 소진, 인증 실패, 잘못된 요청을 같은 상태로 취급하면 정상 경로까지 차단하거나 잘못된 요청을 반복하게 된다.

### 6.3 quota 관측과 quota에 따른 선택은 구분돼 있다

`sdk/cliproxy/auth/types.go:344`의 `QuotaState`는 두 종류의 정보를 담는다. `Exceeded`, `NextRecoverAt`, `BackoffLevel`은 scheduler용 상태다. `ObservedAt`, `Signals`는 최근 공급자 응답에서 관측한 quota snapshot이다.

`sdk/cliproxy/auth/quota_signals.go:18`은 passive quota observation을 지원하는 내장 provider로 Claude·Codex·Devin을 나열한다. `quota_signals.go:38`의 `ObserveResponseHeadersForProvider()`는 response header의 관측값만 갱신하며 cooldown 필드를 직접 바꾸지 않는다. 최신 응답 snapshot으로 교체하므로 과거의 limit-reached 값이 무한히 누적되는 방식도 아니다.

관리 계층에는 능동 조회 확장도 있다. `internal/api/handlers/management/plugin_quota.go:63`의 `FetchCredentialQuota()`는 plugin QuotaProvider에 조회를 위임하거나 auth metadata의 declarative `quota_probe`를 실행한다. `internal/pluginhost/quota_provider.go:77`의 `QuotaProviders()`는 활성 quota provider를 설명한다. 따라서 CLIProxyAPI 전체에 잔량 조회 기능이 없다고 말하면 틀리다.

반면 내장 selector 본문은 quota 잔여 비율을 모든 업체 공통 단위로 정렬하는 알고리즘이 아니다. priority·weight·RR·session binding·cooldown을 적용한다. **잔량 조회가 존재한다는 사실만으로, 잔량을 기반으로 업체 우선순위가 자동 재조정된다고 결론 내릴 수는 없다.** plugin scheduler나 Home이 추가 정책을 구현할 수 있는 구조다.

### 6.4 alias와 모델 pool

`sdk/cliproxy/auth/conductor_models.go:401`의 `executionModelCandidatesWithAlias()`는 요청 alias를 auth별 upstream 모델로 해석한다. OpenAI-compatible pool에 여러 모델이 있으면 시작 위치를 회전한다. `conductor_models.go:396`은 실행 전 후보별 cooldown을 다시 필터링한다.

이 기능은 대체 후보를 구성하는 기반이다. 다만 다른 모델이 동일한 품질·도구·이미지·context 능력을 가진다는 보증은 아니다. 모델 교체 허용 범위는 사용자 정책과 모델 capability에 따라 결정해야 한다.

### 6.5 0원 전용 프로그램과의 차이

분석한 native selection 경로에는 공급자 가격과 무료 잔량을 모두 확정한 뒤 0원 경로만 허용하는 공통 price gate가 보이지 않는다. API key와 OAuth 자격증명을 함께 취급하는 범용 proxy이며, 연결된 자격증명의 플랜과 upstream 정책을 가격 판단으로 대체하지 않는다.

오히려 `sdk/cliproxy/auth/conductor_execution.go:270`에는 일반 경로 소진 후 Antigravity credits fallback을 검토하는 분기가 있다. `sdk/cliproxy/auth/conductor_home.go:1440`의 조건은 설정의 `QuotaExceeded.AntigravityCredits`를 요구하며, 특정 실패에서만 진입한다. 이는 **명시적 설정을 통해 credit 경로를 사용하는 범용 게이트웨이**라는 근거다. 이 코드만으로 자동 결제나 실제 청구액을 단정하는 것은 별개 문제다.

Freeway가 무료 사용을 제품의 중심에 둔다면 “API를 연결했다”에서 끝내지 말고, 무료 판단 근거·회복시각·허용한 fallback·유료 경로 진입 차단을 사용자에게 명확하게 제공해야 한다. 이것은 이 분석에서 도출한 제품 제안이며, CLIProxyAPI의 모든 plugin에 기능이 없다는 단정은 아니다.

## 7. OAuth refresh는 요청 재시도와 별도 문제다

`sdk/cliproxy/auth/conductor_refresh.go:1155`의 `tryRefreshAfterUnauthorized()`는 request-scoped 오류를 먼저 제외한다. 일반 401이면 실패한 access token을 거부 표시하고, refresh credential이 있으며 같은 요청에서 아직 갱신하지 않았다면 갱신을 시도한다. 갱신된 token이 여전히 거부된 token이면 재실행하지 않는다.

`sdk/cliproxy/auth/conductor_execution.go:780` 부근은 refresh 성공 시 같은 auth로 executor를 한 번 더 실행한다. 이후 결과를 기록하고 다른 auth나 모델 후보로 넘어갈지 결정한다. 따라서 refresh와 계정 fallback이 뒤섞인 무한 재시도 방식이 아니다.

`internal/runtime/executor/claude_executor_auth.go:149`는 refresh token으로 OAuth endpoint를 호출한 뒤 access token, refresh token, 만료시각, 계정 정보 등을 갱신한다. `internal/runtime/executor/codex_executor_auth.go:154`는 디스크 경로가 있으면 파일 lock을 획득하고 최신 디스크 자격증명과 병합한다. Codex의 outer token과 SIWC token도 별도 상태로 다룬다.

`internal/auth/codex/refresh_journal.go:132`의 `journalRefresh()`는 grant를 전송하기 전에 claim을 기록한다. 이미 처리된 refresh는 receipt를 읽어 재사용하고, 전송 결과가 불명확한 grant를 임의로 재전송하지 않도록 구분한다. 이 계층의 어려움은 단순 만료시간 비교보다 refresh token rotation·프로세스 재시작·동시 요청·응답 유실에 있다.

관련 테스트 본문은 `internal/auth/codex/refresh_outcome_test.go:128`의 redirect replay 방지와 `:145`의 429/503 이후 grant 재전송 방지를 검증하도록 작성돼 있다. 이는 테스트가 검증하려는 계약을 읽은 결과이며, 이 문서 작성 과정에서 테스트를 실행한 결과는 아니다.

## 8. 스트리밍은 어디까지 자동 복구하는가

### 8.1 첫 전달 가능한 payload 이전

`sdk/api/handlers/handlers_stream.go:548`의 `readInitialStreamChunks()`는 첫 전달 가능한 payload가 나올 때까지 읽는다. 빈 chunk나 interceptor에서 전달하지 않은 chunk는 초기 준비 단계에 남는다.

`handlers_stream.go:580`의 `bootstrapEligible`은 초기 401·402·403·408·429와 5xx 등을 재시도 대상으로 분류한다. `handlers_stream.go:594`에서 bootstrap retry 상한을 읽고, `:608`에서 auth manager의 `ExecuteStream()`을 다시 호출한다. 재시도에 성공하면 응답 header와 validator 상태도 새 시도에 맞춰 교체한다.

Home 모드는 이 handler 계층의 bootstrap retry를 0으로 만든다. 원격 dispatch의 실행 생명주기와 중복되지 않도록 별도 처리가 있는 구조다.

### 8.2 이미 payload를 전달한 이후

`handlers_stream.go:707`은 초기 payload를 downstream channel로 보낸다. 이후 `handlers_stream.go:741`의 `chunk.Err`는 오류를 전달하고 처리를 종료한다. 다른 provider에 처음부터 요청해 이미 보낸 부분을 몰래 이어 붙이는 동작은 이 루프에 없다.

소스의 테스트 계약도 이를 구분한다.

| 테스트 | 기대하는 동작 | 위치 |
|---|---|---|
| `TestExecuteStreamWithAuthManager_RetriesBeforeFirstByte` | 첫 byte 이전 오류에서는 두 번 실행하고 두 번째 시도의 header와 결과를 사용한다. | `sdk/api/handlers/handlers_stream_bootstrap_test.go:332` |
| `TestExecuteStreamWithAuthManager_DoesNotRetryAfterFirstByte` | `partial` 전달 후 오류에서는 한 번만 실행하고 terminal 오류를 전달한다. | `sdk/api/handlers/handlers_stream_bootstrap_test.go:857` |

Freeway도 이 경계를 설계해야 한다. 출력 전 재시도는 클라이언트에 숨길 수 있지만, 출력 후에는 기존 답변·도구 호출·사용량이 이미 발생했을 수 있다. “한도 초과면 다른 LLM으로 자동 요청”이라는 목표에 스트리밍 중간 복구까지 포함하려면 별도의 사용자 경험과 재개 계약이 필요하다.

## 9. 사용량과 응답 변환

`internal/runtime/executor/helps/usage_helpers.go:85`의 `NewExecutorUsageReporter()`는 executor별 reporter를 생성한다. reporter에는 provider, 모델, alias, auth, 요청·session identity, streaming 여부, 첫 응답 지연 등 실행 맥락이 들어간다.

`usage_helpers.go:361`의 `Publish()`와 `:576`의 `PublishFailure()`는 성공·실패 사용량을 공통 record로 만든다. `usage_helpers.go:643`에서 `usage.PublishRecord()`로 내보낸다. 모델별 executor가 각자 임의의 형식으로 사용량을 쌓는 구조가 아니다.

모델 alias와 upstream의 실제 모델은 다를 수 있다. reporter는 실제 응답 모델과 요청 모델을 구분하며, upstream이 다른 모델을 반환한 경우 경고를 남기는 경로도 있다. 무료 fallback의 품질을 사용자에게 설명하려면 이런 실제 serving model 기록이 필요하다.

변환 계층도 독립 책임이다. `sdk/translator/registry.go:114`는 request envelope 변환, `:264`는 stream 변환을 제공한다. 공급자 executor는 변환 후 thinking·payload 규칙을 적용하고 HTTP framing을 생성한다. 동일한 OpenAI 호환 진입점을 제공해도 공급자의 native tool·reasoning·signature·compaction 의미가 자동으로 같아지는 것은 아니다.

## 10. 로컬 모델 지원은 무엇을 뜻하는가

### 10.1 `--local-model`은 로컬 추론 엔진 옵션이 아니다

`cmd/server/main.go:766`은 이 옵션의 의미를 “별도 catalog source가 없으면 내장 catalog 사용”이라고 출력한다. `internal/registry/catalog_sources.go:384`의 `SetLocalModelCatalogs()`는 catalog 선택 정책을 저장한다. `catalog_sources.go:501`의 `effectiveCatalogSources()`는 local일 때 비어 있는 source를 embedded source로 바꾼다.

따라서 이 플래그를 켠다고 모델 weight를 내려받거나 GPU에 올려 실행하는 것이 아니다. 이름에 `local-model`이 있다는 이유로 Ollama 같은 추론 런타임이라고 해석하면 틀리다.

### 10.2 로컬 inference server 연결은 custom endpoint 방식이다

`internal/runtime/executor/openai_compat_executor.go:1143`의 `resolveCredentials()`는 auth attribute의 `base_url`과 `api_key`를 읽는다. `openai_compat_executor.go:113`의 `executeWithCredentials()`는 OpenAI 호환 protocol로 요청을 변환하고, `:182` 부근에서 base URL과 `/chat/completions`를 합쳐 HTTP 요청을 만든다. API key가 비어 있으면 Bearer header를 추가하지 않는 분기도 있다.

따라서 사용자 PC의 OpenAI-compatible inference server를 base URL로 지정하는 구성이 가능하다. 실행 주체는 별도로 떠 있는 inference server이며, CLIProxyAPI는 HTTP 게이트웨이다. 이 native executor 자체가 GGUF weight를 로드하거나 GPU 자원을 관리하지는 않는다.

이것만으로 “모든 로컬 모델을 무조건 지원한다”고 말할 수는 없다. 실제로는 endpoint protocol과 입력·출력·stream·tool 형식의 호환성이 필요하다. Freeway의 로컬 모델 목표도 로컬 server 연결, 설치·기동 관리, weight 관리 중 어디까지 담당할지 구분해야 한다.

## 11. 영속화·관리 API·TUI

### 11.1 자격증명 저장은 provider별 차이가 있다

`sdk/auth/filestore.go:75`의 `FileTokenStore.Save()`는 auth 경로를 해석하고 공통 저장 흐름을 제공한다. `filestore.go:104`에서 디렉터리를 0700으로 생성한다. provider별 `Storage`가 있으면 `filestore.go:127`에서 그 구현에 저장을 위임한다. 일반 metadata JSON 분기는 `filestore.go:140`, `:152`에서 0600 파일을 사용한다.

Codex의 `internal/auth/codex/token.go:63`은 0600 임시파일에 JSON을 쓰고 rename하는 atomic replacement를 사용한다. `token.go:271`의 `SaveTokenToFile()`는 파일 lock 아래에서 기존 JSON과 새 token 정보를 병합한다.

Claude의 `internal/auth/claude/token.go:73`은 JSON을 쓰지만 `:89`에서 `os.Create()`를 사용한다. 이 함수 자체는 Codex와 같은 임시파일·0600 chmod·atomic rename 절차를 적용하지 않는다. 상위 디렉터리 권한과 시스템 umask의 영향이 있으므로 “공급자와 무관하게 모든 token 파일을 동일한 0600 atomic 저장으로 처리한다”는 설명은 정확하지 않다.

Codex와 Claude token 구조체는 access token과 refresh token을 JSON 필드로 저장한다. 여기서 읽은 기본 파일 저장 함수에는 OS keychain을 사용하는 저장이나 token 내용을 암호화하는 절차가 없다. 개인 PC 게이트웨이의 credential 저장 방식을 결정할 때 실제로 비교할 지점이다.

`internal/watcher/watcher.go:159`와 `sdk/cliproxy/service_lifecycle.go:211`은 설정·auth 디렉터리 변경을 runtime에 반영하는 연결점이다. 파일을 수정한 뒤 서버를 재시작해야만 계정을 추가할 수 있는 구조는 아니다.

### 11.2 관리 API와 inference API는 분리돼 있다

현재 v8 관리 계약은 `internal/api/server_management_v8.go:12`의 `registerManagementV8Routes()`에 있다. 기본 prefix는 `/v8/management`다.

| 관리 영역 | 제공 경로 예 | 역할 |
|---|---|---|
| 설정 | `/config`, `/config/*path` | 전체·부분 설정 조회 및 변경 |
| 라우팅 | `/routing/cooldown/reset`, `/models/refresh` | cooldown 초기화·catalog 갱신 |
| 관측 | `/observability/usage/api-keys`, `/observability/requests/in-flight` | 사용량과 실행 중 요청 조회 |
| 자격증명 | `/credentials`, `/credentials/refresh`, `/credentials/verify` | 추가·삭제·상태·갱신·검증 |
| OAuth | `/oauth/auth-url`, `/oauth/status`, `/oauth/session` | 로그인 시작·진행·취소 |
| plugin | `/plugins`, `/plugins/store`, `/plugins/:id/quota` | plugin 및 quota 확장 관리 |

기존 `/v0/management` 등록도 `internal/api/server_management.go:14`에 남아 있지만, 저장소의 `AGENTS.md`는 이를 유지보수 대상에서 제외된 deprecated 경로로 명시한다. 새 설계의 기준은 v8 contract로 잡는 편이 맞다.

`internal/api/handlers/management/handler.go:351`의 middleware는 `Authorization` 또는 `X-Management-Key`를 읽는다. `handler.go:386`의 `AuthenticateManagementKey()`는 원격 허용 여부, local 여부, 실패 횟수에 따른 차단, 관리 키를 확인한다. localhost 요청도 관리 키 검증을 거친다. `MANAGEMENT_PASSWORD`가 있으면 원격 허용 override도 적용된다.

관리 API는 credential 다운로드·설정 변경·plugin 설치처럼 inference 호출보다 넓은 권한을 갖는다. 두 인증 경계를 분리한 구조를 Freeway에서도 유지할 이유가 여기에 있다.

### 11.3 TUI는 관리 클라이언트다

`cmd/server/main.go:768` 이후 standalone TUI 모드는 로컬 API 서버를 함께 시작한다. `main.go:840`에서 TUI를 실행한다. 일반 TUI 모드는 `main.go:857`에서 설정된 management base URL에 연결한다.

따라서 TUI는 provider executor 안에 붙은 독립 추론 로직이 아니라 관리 API를 사용하는 클라이언트다. Freeway에서 A가 UI·연결 과정을 맡고 B가 routing·quota·session을 맡는다면 참고할 수 있는 분리다.

## 12. Freeway와 겹치는 부분과 남는 설계 과제

| Freeway 아이디어 | CLIProxyAPI에서 읽은 구현 | 판단 |
|---|---|---|
| 사용자 PC를 게이트웨이로 사용 | 설정된 host/port의 로컬 서버, standalone TUI | 그대로는 차별점이 되기 어렵다. |
| 한 주소로 여러 업체 사용 | OpenAI·Claude·Gemini 진입 API와 provider executor | 이미 상당히 구현돼 있다. |
| API key·OAuth 간편 연결 | 자체 OAuth, 관리 API의 로그인·credential 관리 | 이미 존재한다. 연결 UX의 단순화는 별도 개선 대상이다. |
| 한도 초과 시 다른 계정 사용 | 가용성 필터·auth rotation·retry·cooldown | 이미 핵심 기능으로 존재한다. |
| 업체 우선순위 변경 | priority·weight·fill-first·plugin scheduler | 수동 정책과 확장 기반이 존재한다. |
| 사용량·quota 표시 | usage reporter·passive snapshot·plugin quota 조회 | 기능 존재 여부보다 정확도와 정책 연결을 비교해야 한다. |
| 로컬 모델 사용 | OpenAI-compatible custom base URL | 로컬 추론 서버가 제공하는 API에 연결하는 방식이다. |
| 공식 코딩 CLI 간 작업 이어가기 | 추적한 native inference 경로는 HTTP/WS proxy | 별도의 agent orchestration과 session 이전 계약이 필요하다. |
| 무료 잔량 중심의 자동 경로 선택 | 내장 선택은 priority/RR/weight/cooldown 중심 | 0원 정책·잔량 정규화·회복시각 활용을 별도 제품 목표로 정의할 여지가 있다. |

이 분석에서 Freeway의 역할 분리를 도출하면 다음과 같다. 이는 CLIProxyAPI 코드의 기능 목록이 아니라 비교 결과를 바탕으로 한 제안이다.

**A: 연결과 사용 경험을 맡는다.** 공급자를 공식 API·OAuth HTTP·로컬 endpoint·외부 CLI 실행으로 구분하고, 각 연결 방식의 설치·로그인·연결 검사·모델 목록·사용 가능 기능을 관리한다. CLI/TUI에서 계정 추가와 정책 설정을 제공하며, 어떤 이유로 어느 경로가 선택됐는지 보여 준다. OAuth callback과 token 획득 방식은 B와 먼저 계약해야 한다.

**B: 실행 정책과 상태를 맡는다.** A가 제공한 credential과 capability를 받아 가용성·우선순위·무료 정책·쿼터 회복·retry·stream commit 경계를 적용한다. token refresh와 동시성, 사용량 정산, credential 저장, session binding을 책임진다. 외부 코딩 CLI를 실행할 계획이라면 HTTP executor와 별도 계약으로 프로세스 수명주기·workspace·중단·재개를 다룬다.

두 사람이 먼저 합의해야 할 공통 계약은 `ProviderAdapter`, `Credential`, `ModelCapability`, `QuotaSnapshot`, `RouteDecision`, `ExecutionResult`의 의미다. 특히 quota snapshot의 관측시각·출처·확정값/추정값과, streaming 결과를 언제 사용자에게 확정 전달하는지부터 정의해야 한다. 범용 proxy의 기능 수를 따라가는 것보다 Freeway가 보장할 행동을 먼저 고정하는 편이 작은 팀에 맞다.

## 13. 근거의 범위

분석은 서버 시작, 기본 local inference, Codex·Claude OAuth, Gemini HTTP, auth selector, quota 상태 전이, 초기 streaming 복구, 파일 저장, 관리 API, plugin 연결점의 소스에 근거한다. 테스트 파일은 검증하려는 계약을 읽었고, 문서 작성 중 테스트나 실계정 요청은 실행하지 않았다. 별도로 시도한 실행 검증은 의존성 준비 중 중단했다. 이 프로젝트의 테스트 통과를 주장하지 않는다.

Home 제어 서버의 독립 저장소와 동적으로 설치되는 외부 plugin의 구현 전체는 이 문서의 대상이 아니다. 따라서 **“내장 경로에서 보이는 동작”과 “확장하면 구현할 수 있는 동작”을 구분**한다. 소스의 HTTP 경로가 존재한다는 사실은 특정 계정의 현재 무료 허용량, 공급자의 계정별 승인 정책, 서비스 약관을 판정하는 근거가 아니다.

이 파일을 작성하는 동안 `cliproxyapi`의 소스는 변경하지 않았다.
