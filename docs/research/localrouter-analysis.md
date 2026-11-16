# LocalRouter 소스 구조 분석

조사 기준: `localrouter`, commit `5ecfce7d74f207965278c490c825b730fd84edbc`. README 기능 소개가 아니라 요청 처리 구현과 테스트 본문을 추적했다. 저장소 코드는 변경하지 않았으며, 이 문서 작성 과정에서는 테스트를 실행하지 않았다.

## 1. Freeway 관점의 결론

LocalRouter는 이미 사용자의 PC에서 실행되는 게이트웨이이며, 로컬 모델과 외부 API를 같은 입구로 연결한다. 무료 모델 판정, 무료 한도 검사, 429 이후 다른 모델로 이동하는 자동 라우팅, 구독 OAuth, CLI 에이전트 실행까지 구현되어 있다. 따라서 **“PC가 게이트웨이가 된다”, “무료 업체들을 모은다”, “한도가 차면 다음 모델로 넘어간다”만으로 Freeway를 차별화하기는 어렵다.**

다만 서로 다른 기능을 하나로 묶어 이해하면 안 된다. LocalRouter의 일반 LLM API 라우팅, OpenCode 같은 앱의 설정 변경, 공식 코딩 CLI의 프로세스 실행은 각각 별도 경로다. 공식 CLI가 목록에 있다는 사실만으로 “OpenCode 무료량이 끝나면 진행 중인 코딩 작업을 Cline으로 이어간다”까지 구현됐다고 볼 수 없다. 이번에 추적한 일반 completion 경로의 fallback은 provider/model 후보 사이에서 일어난다.

Freeway가 검토할 만한 차이는 무료 잔량의 동시 요청 예약, 관측된 구독 잔량을 실제 우선순위에 연결하는 정책, 작업 중단 없이 다른 에이전트로 넘기는 세션 규약, 연결과 장애 원인을 쉽게 설명하는 CLI 경험이다. 이 항목은 LocalRouter의 모든 경로에 기능이 없다는 선언이 아니라, 아래에서 확인한 구현 경계로부터 도출한 Freeway 검증 과제다.

## 2. 실제 폴더 구조와 책임

```text
localrouter/
├── src/                         프런트엔드
├── src-tauri/                   데스크톱 진입점, 서비스 조립, 앱 연동
│   └── src/launcher/integrations/ 외부 앱의 설정 및 실행 연동
├── crates/
│   ├── lr-server/               HTTP 입구, 인증, 요청 파이프라인, 응답
│   ├── lr-router/               전략, 후보 선택, fallback, 한도, 사용량
│   ├── lr-providers/            업체별 API와 공통 provider 인터페이스
│   ├── lr-config/               설정 타입, 검증, 저장, 변경 감지
│   ├── lr-clients/              게이트웨이에 접속하는 클라이언트 관리
│   ├── lr-api-keys/             키 저장소와 keychain 추상화
│   ├── lr-oauth/                OAuth 공통 지원
│   ├── lr-coding-agents/        코딩 CLI 프로세스와 세션 관리
│   ├── lr-usage/                구독 및 API 계정의 사용 한도 관측
│   ├── lr-monitoring/           메트릭 수집과 영속성
│   ├── lr-monitor/             요청·인증·라우팅 이벤트
│   ├── lr-proxy/               프록시 기능
│   ├── lr-engines/             추론 엔진 관련 기능
│   ├── lr-local-models/        로컬 모델 관련 기능
│   ├── lr-mcp/                 MCP 기능
│   ├── lr-mcp-via-llm/         LLM 경로와 MCP 연결
│   └── ...                    cache, context, memory, replay, batch 등
├── tests/
├── docs/
├── scripts/
├── packaging/
└── website/
```

위 디렉터리는 실제 파일 시스템에서 확인했다. Rust workspace의 구성원은 [Cargo.toml:1](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/Cargo.toml#L1)에 명시되어 있다. 실행 시 provider registry, rate limiter, metrics collector, free-tier manager를 `Router::new`에 주입하는 조립 지점은 [main.rs:610](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/src-tauri/src/main.rs#L610)이다. 저장소가 크다는 이유로 이 모든 기능이 Freeway MVP에 필요하다는 뜻은 아니다.

## 3. 실제 요청 흐름

```text
외부 앱 또는 CLI
  → /v1/chat/completions
  → AuthLayer: Bearer client secret 검증
  → chat_completions_with_endpoint
  → run_turn_pipeline
      요청 검증 → 접근 권한 → client quota
      → 비밀정보 검사 → guardrail
      → 선택적 압축 및 라우팅 분류 → provider 요청 변환
  → Router::complete / stream_complete
      client → strategy 조회
      → 특정 모델 또는 localrouter/auto 분기
      → 후보 필터 → backoff → 무료 판정 → strategy 한도
  → execute_request / dispatch_stream
  → provider 구현의 HTTP 호출
  → 응답 또는 stream
  → 사용량·비용 정산, 메트릭 및 라우팅 설명
```

### 3.1 입구와 인증

`lr-server`는 `/v1/chat/completions`, `/v1/messages`, `/v1/responses`, embeddings와 models 등을 등록하고 `AuthLayer`를 부착한다. [lr-server/src/lib.rs:378](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/lib.rs#L378), [lr-server/src/lib.rs:461](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/lib.rs#L461)

`AuthLayer`의 일반 LLM 요청은 Bearer 토큰을 `client_manager.verify_secret`에 넘긴다. 인증된 client ID를 `AuthContext.api_key_id`로 넣어 이후 라우팅에 사용한다. 내부 테스트 토큰은 별도 우회 분기다. 이 내부 토큰의 존재만으로 외부에서 인증을 우회할 수 있다고 판단할 수는 없다. [auth_layer.rs:235](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/middleware/auth_layer.rs#L235), [auth_layer.rs:256](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/middleware/auth_layer.rs#L256)

MCP의 `client_auth` 경로는 OAuth token store 검증과 client secret 검증을 따로 지원한다. 이 로컬 클라이언트 인증을 OpenAI·Anthropic 등에 로그인하는 upstream OAuth와 구분해야 한다. [client_auth.rs:169](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/middleware/client_auth.rs#L169)

### 3.2 공통 파이프라인

`chat_completions_with_endpoint`는 `run_turn_pipeline`을 호출한다. 구현 본문은 요청 검증과 모델 접근 검사 뒤 rate limit, secret scan, guardrails, 압축 및 라우팅 분류를 수행한다. 오래된 파일 상단 주석에는 통합 entry point가 향후 작업이라고 적혀 있지만, 실제 아래쪽에는 함수가 구현되어 있다. 문서보다 본문을 기준으로 읽어야 하는 사례다. [chat.rs:100](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/routes/chat.rs#L100), [pipeline.rs:2625](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/routes/pipeline.rs#L2625), [pipeline.rs:2743](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/routes/pipeline.rs#L2743), [pipeline.rs:2807](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/routes/pipeline.rs#L2807)

duplicate gateway hop에서는 이미 처리한 스캔·한도·재작성을 반복하지 않는 별도 경로가 있고, native document 및 server tool 요청은 조합 제약도 검사한다. 따라서 요청 종류에 따라 동일한 처리가 무조건 반복되는 구조는 아니다. [pipeline.rs:2635](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/routes/pipeline.rs#L2635), [pipeline.rs:2700](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-server/src/routes/pipeline.rs#L2700)

### 3.3 client, strategy, auto 후보

`validate_client_and_strategy`는 client의 활성 상태를 검사하고 `client.strategy_id`로 전략을 찾는다. `localrouter/auto` 요청은 자동 라우팅으로 들어가고, 특정 모델 요청은 provider/model을 해석해 허용 여부를 검사한다. **특정 모델을 지명한 모든 요청이 자동으로 전체 업체를 순회하는 것은 아니다.** [lr-router/src/lib.rs:1011](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1011), [lr-router/src/lib.rs:2651](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L2651)

`select_models_for_auto_routing`은 정책이 없으면 `prioritized_models`를 사용한다. 정책이 있으면 분류 결과의 route로 후보를 정한다. 이어 `order_auto_models`를 호출하므로 고정 배열만 순회하는 구현보다 발전되어 있다. [lr-router/src/lib.rs:1632](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1632), [lr-router/src/lib.rs:1695](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1695)

분류 모델도 native `SystemOne` 경로를 통하며, 고정 provider/model로 호출해 재귀적인 auto routing을 방지한다. 사용량 기록과 무료 제약을 통과하는 공통 실행 경로를 이용한다. [decision_routing.rs:280](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/decision_routing.rs#L280), [decision_routing.rs:333](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/decision_routing.rs#L333)

### 3.4 provider 실행과 fallback

자동 경로는 후보별로 적합성, 최근 backoff, 무료 조건, 비용 backoff, strategy rate limit을 검사한 뒤 `execute_request`를 호출한다. 성공하면 세션 선택을 기록하고 라우팅 설명에 후보·시도·비용·선택 모델을 넣는다. [lr-router/src/lib.rs:1752](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1752), [lr-router/src/lib.rs:1841](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1841)

`execute_request`는 provider registry에서 실제 인스턴스를 얻고, 모델명과 metadata를 조정하고, feature adapter 및 cache를 처리한 뒤 `messages::dispatch_complete`로 넘긴다. [lr-router/src/lib.rs:1410](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1410), [lr-router/src/lib.rs:1466](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1466), [lr-router/src/lib.rs:1534](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1534)

재시도 대상은 rate limit, 도달 불가, context 초과, policy violation, endpoint 미지원이다. 모든 오류를 무조건 다음 업체로 보내지 않는다. server tool이나 파일 입력이 있으면 오류가 재시도 가능한 종류여도 다음 모델로 이어가지 않는 분기가 있다. [lr-router/src/lib.rs:83](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L83), [lr-router/src/lib.rs:1893](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1893), [lr-router/src/lib.rs:1929](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1929)

## 4. 무료 판정, quota, 429와 동시성

### 4.1 무엇을 무료로 보는가

`classify_for_free_tier`는 provider의 effective free-tier 설정과 모델의 무료 판정을 합친다. 모델 판정은 가격 override, 제외/포함 목록, 활성 rule 등으로 정해진다. 즉 공급자가 매 요청 직전 무과금을 보증해 주는 인터페이스는 아니다. [lr-router/src/lib.rs:772](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L772), [free_models.rs:229](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/free_models.rs#L229)

| 종류 | 호출 전 판정 |
|---|---|
| `AlwaysFreeLocal` | 모델 무료 판정이 참이면 허용 |
| `Subscription` | 기존 구독 안에서 무료로 분류하며, 모델 무료 판정이 참이면 허용 |
| `FreeModelsOnly` | 모델 무료 판정과 설정된 RPM 검사 |
| `RateLimitedFree` | RPM/RPD/TPM/TPD/월별 호출·토큰 한도 검사 |
| `CreditBased` | credit 잔량 검사 |
| `None` | 무료로 분류하지 않음 |

위 분기는 [free_tier.rs:479](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/free_tier.rs#L479)에 있다. `Subscription`은 “구독료까지 0원”이라는 의미가 아니다. 일반 `FreeTierManager.record_usage`에서는 구독 유형의 카운터를 올리지 않는다. [free_tier.rs:862](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/free_tier.rs#L862)

### 4.2 무료 잔량의 확인과 예약은 다르다

`check_rate_limit_capacity`는 tracker lock을 잡고 만료 window와 현재 잔량을 확인한다. 실제 무료 사용량은 응답이 끝난 뒤 `completed_usage`에서 기록한다. 두 단계 사이에 upstream 호출이 있다. [free_tier.rs:535](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/free_tier.rs#L535), [lr-router/src/lib.rs:1337](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1337)

따라서 이 일반 completion 경로에서 잔여 1회를 두 동시 요청이 각각 확인하고 둘 다 출발할 수 있다. tracker에 lock이 있다는 사실만으로 요청 전체의 원자적 quota 예약이 보장되지는 않는다. `adaptive::reserve_inference_dispatch`는 한 turn의 추론 시도 예산이며, 무료 provider quota를 미리 차감하는 예약과 별개다. 이 결론은 호출 전 검사와 호출 후 기록의 분리에 대한 소스 해석이며, 이번 조사에서 동시 부하 실험을 실행한 결과는 아니다.

### 4.3 무료 모드에도 사후 비용 감시가 있다

`completed_usage`는 free-only 요청의 실제 비용이 양수이면 provider의 비용 backoff를 건다. 첫 5분, 반복 시 두 배, 최대 24시간이며 만료 뒤에는 다시 probe를 허용한다. 따라서 현재 구현은 “예상 밖 비용 발생을 감지해 이후 호출을 억제”할 수 있지만, 이미 수행한 첫 요청의 비용을 없애지는 못한다. [lr-router/src/lib.rs:1351](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1351), [free_tier.rs:1191](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/free_tier.rs#L1191)

### 4.4 429·402 이후 동작

`record_rate_limit_error`는 Retry-After, reset header, credit exhaustion, 일반 지수 backoff 순으로 시간을 정한다. credit exhaustion은 5분부터 최대 24시간, 일반 오류는 1초부터 최대 60초다. 모델별 backoff와 provider 전체 Retry-After가 함께 존재한다. 다음 요청은 backoff 상태의 후보를 건너뛰고 다른 후보를 시도한다. [free_tier.rs:927](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/free_tier.rs#L927), [free_tier.rs:992](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/free_tier.rs#L992), [lr-router/src/lib.rs:1771](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L1771)

### 4.5 구독 사용량 대시보드와 라우터의 연결 범위

`lr-usage`는 proxy/gateway 응답 header와 사용자 허용 하의 usage endpoint 조회를 계정별로 합친다. `UsageObserverMiddleware`는 provider HTTP 응답을 관측한다. [lr-usage/src/lib.rs:1](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-usage/src/lib.rs#L1), [http_client.rs:35](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/http_client.rs#L35)

반면 이번에 읽은 `lr-router`의 무료 게이트와 후보 순회는 `FreeTierManager`에 연결되어 있다. 구독 사용량을 화면에 보인다는 사실만으로 그 값이 자동 라우팅 순위를 뒤집는다고 간주해서는 안 된다. Freeway의 목표가 “실제 잔량과 reset 시간을 보고 업체 우선순위를 조정”이라면, 관측·정책·예약·정산을 한 흐름으로 검증하는 것이 중요하다.

## 5. 스트리밍 실패의 경계

자동 streaming 경로는 `dispatch_stream` 자체가 오류를 반환할 때 다음 후보를 시도할 수 있다. 그러나 stream 객체를 성공적으로 얻으면 `observe_stream`, routing metadata, usage tracking wrapper를 붙여 바로 반환한다. 그 뒤 개별 chunk에서 오류가 발생해도 현재 후보 루프 안으로 돌아오지는 않는다. 이미 출력한 텍스트를 취소하고 다른 모델의 응답으로 교체하는 기능과는 다르다. [lr-router/src/lib.rs:2231](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L2231), [lr-router/src/lib.rs:2242](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L2242)

사용량 정산은 세심하게 분리되어 있다. `StreamUsageRecorder`는 한 번만 상태를 꺼내 정산하고, drop 시에도 `finish`한다. upstream이 사용량을 보고하면 이를 사용하고, 없으면 prompt 및 출력 문자 수로 추정한다. 취소된 stream에도 알려진 사용량을 남기려는 구조다. 다만 추정치는 공급자 청구서와 동일한 보증이 아니다. [lr-router/src/lib.rs:393](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L393), [lr-router/src/lib.rs:447](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L447), [lr-router/src/lib.rs:453](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/lib.rs#L453)

## 6. 로컬 모델, OAuth HTTP와 공식 CLI 실행의 구분

| 연결 | 실제 구현 | 의미 |
|---|---|---|
| Ollama | `http://localhost:11434/api/chat` HTTP | 로컬 엔진에 요청. 매 추론마다 Ollama CLI를 실행하지 않음 |
| ChatGPT 구독 provider | `chatgpt.com/backend-api/codex` Responses HTTP | OAuth token으로 backend 직접 요청. Codex CLI 실행과 다름 |
| OpenCode Zen/Go provider | OpenAI-compatible HTTP endpoint | API key를 가진 별도 provider |
| OpenCode 앱 integration | 앱 설정 및 proxy plugin 생성 | OpenCode를 LocalRouter의 클라이언트로 연결 |
| coding agents | 외부 executor 또는 직접 subprocess | 실제 설치된 코딩 CLI를 실행하고 세션 관리 |

Ollama는 custom base URL을 지원하고 API key가 필요 없으며 가격을 free로 반환한다. [ollama.rs:44](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/ollama.rs#L44), [ollama.rs:623](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/ollama.rs#L623)

ChatGPT 구독 provider는 요청을 Responses 형태로 바꾸고 token으로 HTTP 요청한다. 401이면 새 token을 얻어 한 번 더 요청한다. [openai.rs:24](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/openai.rs#L24), [openai.rs:1030](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/openai.rs#L1030)

`OpenCodeZenProviderFactory`는 API key를 받아 `https://opencode.ai/zen/v1`을, `OpenCodeGoProviderFactory`는 `https://opencode.ai/zen/go/v1`을 사용하는 `OpenAICompatibleProvider`를 만든다. 앱 설정 integration과 같은 기능이 아니다. [factory.rs:3146](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/factory.rs#L3146), [factory.rs:3222](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/factory.rs#L3222), [integrations/opencode.rs:1](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/src-tauri/src/launcher/integrations/opencode.rs#L1)

실제 CLI 실행도 구현되어 있다. 지원 enum은 11종이고, `spawn_via_executor`가 session 환경과 승인을 준비해 executor의 `spawn` 또는 `spawn_follow_up`을 호출한다. Aider와 Antigravity는 별도 direct 경로로 `tokio::process::Command`를 실행한다. executor는 고정 revision의 외부 `vibe-kanban` crate에 의존한다. 따라서 “전부 HTTP proxy일 뿐”이라는 설명도 틀리다. [types.rs:2521](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-config/src/types.rs#L2521), [manager.rs:963](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-coding-agents/src/manager.rs#L963), [manager.rs:1096](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-coding-agents/src/manager.rs#L1096), [lr-coding-agents/Cargo.toml:35](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-coding-agents/Cargo.toml#L35)

## 7. 설정, 인증정보와 로컬 저장소

release의 기본 설정은 `~/.localrouter/settings.yaml`, debug는 `~/.localrouter-dev`이며 `LOCALROUTER_ENV`로 분리할 수 있다. `ConfigManager`는 공유 설정, 저장 mutex, 파일 변경 감지를 가진다. 설정 저장은 검증, 기존 파일 backup, Unix 0600 임시파일, sync, atomic rename 순서다. [paths.rs:8](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-utils/src/paths.rs#L8), [lr-config/src/lib.rs:48](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-config/src/lib.rs#L48), [storage.rs:237](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-config/src/storage.rs#L237)

| 데이터 | 저장 방식 및 확인 위치 |
|---|---|
| provider API key, 민감한 custom header | `CachedKeychain` 경유. release 기본은 OS keyring, debug 기본은 파일. `LOCALROUTER_KEYCHAIN` override 가능 |
| OAuth credential | keychain 사용과 별개로 access/refresh token을 포함한 credential JSON 사본도 저장 |
| 요청 사용 메트릭 | `metrics.db` SQLite |
| 무료 한도 상태 | `free_tier_state.json` |
| 구독·한도 관측 상태 | `usage_limits_state.json` |

키 저장 backend 조건은 [key_storage.rs:23](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/key_storage.rs#L23), [keychain_trait.rs:550](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-api-keys/src/keychain_trait.rs#L550)에 있다. “모든 환경에서 모든 비밀을 OS keychain에만 보관한다”는 설명은 정확하지 않다.

OAuth의 `OAuthCredentials`에는 `access_token`과 `refresh_token`이 포함되며, 성공한 로그인은 전체 credentials를 `OAuthStorage`에 저장한다. JSON 저장은 Unix 0600 임시파일과 atomic replace를 사용하지만, 이 직렬화 경로 자체에 암호화는 없다. “OAuth가 keychain을 전혀 사용하지 않는다”가 아니라 **keychain과 별개로 token 포함 JSON 사본도 존재한다**는 것이 정확한 결론이다. [oauth/mod.rs:38](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/oauth/mod.rs#L38), [oauth/mod.rs:184](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/oauth/mod.rs#L184), [oauth/storage.rs:72](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/oauth/storage.rs#L72)

각 상태 파일의 실제 초기화는 [main.rs:537](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/src-tauri/src/main.rs#L537)에서 [main.rs:605](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/src-tauri/src/main.rs#L605) 사이에 있다. 로컬 게이트웨이라고 해서 로그나 메트릭을 전혀 저장하지 않는 것은 아니다.

## 8. Freeway에서 가져갈 구조와 별도로 증명할 목표

| 항목 | LocalRouter에서 확인한 중복 | Freeway의 검증 과제 |
|---|---|---|
| 로컬 게이트웨이 | 로컬 HTTP 입구와 외부·로컬 provider | 단순 존재가 아니라 설치·연결에 필요한 단계 수로 차이를 증명 |
| 무료 자동 전환 | 무료 필터와 auto 후보 fallback | 마지막 무료량을 동시 요청이 나눠 쓰는 예약·취소·정산 규약 |
| 구독 사용량 | 별도 계정 관측과 UI용 집계 | 관측된 잔량·reset·모델 역량을 순위 정책에 실제로 연결 |
| 코딩 CLI | 여러 실제 CLI와 세션 실행 | 서로 다른 agent 사이의 작업 상태 이관과 중복 실행 방지 |
| 스트리밍 | stream 생성 오류 fallback, 사용량 정산 | 출력 전/후 실패를 구분하고 사용자에게 재개 선택을 설명 |
| 보안·저장 | client 인증, provider 키 저장 추상화 | 비밀 사본의 위치·보존·삭제 규약과 배포 기본값 |
| 사용자 설명 | 후보 및 시도 metadata | “왜 이 모델인가, 왜 넘어갔는가, 언제 다시 쓸 수 있나”를 CLI에 명료하게 표시 |

MVP를 만들 때는 HTTP 입구, provider adapter, routing policy, quota ledger, credential store, CLI/UI를 책임 단위로 분리할 근거가 충분하다. 반대로 LocalRouter의 MCP·메모리·프록시·marketplace 전체를 첫 버전에 가져올 필요는 없다. Freeway가 먼저 증명할 것은 선택한 무료 경로 두세 개에서 **실제 한도 소진 → 다음 경로 선택 → 사용량 기록 → reset 후 복귀**가 일관되게 동작하는지다.

## 9. 본문을 확인한 테스트와 검증 범위

다음 테스트는 본문을 읽어 검사 목적을 확인했다. 실행은 의존성 준비·컴파일 도중 중단했다. 이 문서에 열거한 테스트의 통과를 주장하지 않는다.

| 테스트 | 검사하는 내용 | 소스 |
|---|---|---|
| `test_rpm_enforcement_blocks_after_limit` | 설정된 RPM을 기록한 뒤 다음 무료 판정을 막음 | [free_tier.rs:1803](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/free_tier.rs#L1803) |
| `test_header_zero_remaining_blocks` | provider header의 remaining=0이 가용성 검사에 반영됨 | [free_tier.rs:2323](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/free_tier.rs#L2323) |
| `test_persist_preserves_all_counters` | 분·일·월 호출/토큰 카운터 저장 및 재로드 | [free_tier.rs:2458](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-router/src/free_tier.rs#L2458) |
| `stream_reports_final_eval_counts` | loopback mock Ollama NDJSON에서 마지막 usage를 수집 | [ollama.rs:1291](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-providers/src/ollama.rs#L1291) |
| `executor_agents_are_not_spawned_directly` | Claude/Codex/OpenCode/Gemini가 direct CLI args 분기를 타지 않음 | [manager.rs:1663](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/crates/lr-coding-agents/src/manager.rs#L1663) |

이 테스트들은 실계정 없이 한도·header·파일·stream parsing 경계를 확인할 수 있다. 그러나 단위 테스트 통과가 실계정의 무료 entitlement나 동시 quota 예약의 정확성을 대신 증명하지는 않는다. 저장소 라이선스는 [Cargo.toml:42](https://github.com/LocalRouter/LocalRouter/blob/5ecfce7d74f207965278c490c825b730fd84edbc/Cargo.toml#L42)의 `AGPL-3.0-or-later`다. 구조를 참고하는 결정과 소스 코드를 복사하는 결정은 구분해야 한다.
