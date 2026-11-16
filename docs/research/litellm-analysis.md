# LiteLLM 소스 구조 분석

분석 기준: 2026-11-16, `BerriAI/litellm`의 `d034fb681ca296a0f082a224043eda62a2ce82d4`. 로컬 저장소는 `litellm`이다. 아래 경로는 이 저장소 기준이다. 배포된 모든 버전이 이 구조라는 뜻은 아니다.

## 1. 프리웨이와 비교한 핵심

LiteLLM은 사용자가 직접 실행할 수 있는 추론 게이트웨이이자 Python SDK다. 로컬 Ollama, 외부 API, 모델 그룹, 공급처 전환, 사용량 기반 선택, 예산과 권한 검사를 이미 갖추고 있다. 따라서 ‘개인 PC에서 여러 모델을 연결하고 실패하면 다른 모델을 호출한다’만으로는 프리웨이의 고유 기능이 되지 않는다.

현재 클론에서는 Python `Router`가 Rust 코어를 생성한다. 다만 전체 서버가 Rust인 것은 아니다. HTTP 인증·정책·공급처 호출·스트림 객체와 여러 선택 전략은 Python에 남고, 선택·재시도·fallback 상태 기계가 Rust와 협력한다. 단순히 ‘Python 라우터’ 또는 ‘Rust 서버’로 요약하면 모두 부정확하다.

근거: [Router 생성](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router.py#L418), [make_core](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_core.py#L51), [PythonHost](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_python_host.py#L108), [선택 경계](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/rust/crates/router-core/src/select.rs#L1).

## 2. 실제 폴더와 책임

```text
litellm/
  litellm/
    proxy/
      proxy_server.py                 HTTP 엔드포인트
      auth/user_api_key_auth.py       프록시 접근 인증
      common_request_processing.py    요청 정규화·정책·응답 처리
      route_llm_request.py            라우터 또는 공급처 호출로 분기
    router.py                        공개 Router 호환 표면
    router_config.py                 설정 모델과 구성 변환
    router_core.py                   코어 계약과 생성
    router_python_host.py            Python 기능을 코어에 제공
    router_rust/
      core.py                        Rust 코어 실행 단계와 Python 연결
      invoke/                        실제 호출·스트림 래퍼
      surface/                       registry·health 등 공개 표면
    rust/crates/router-core/src/
      lib.rs                         Core·스냅샷·상태
      select.rs                      후보 필터와 선택
      call.rs                        시도·재시도·fallback
      call/stream.rs                 스트림 복구 정책
    router_strategy/                 비용·지연·사용량·부하별 선택
    redis/stores/router_usage/       사용량 카운터 저장 계약
    llms/                           공급처별 요청·응답 변환
  tests/                            Python 및 통합 시험
```

이 저장소의 최상위 `litellm/` 안에 Python 패키지와 Rust workspace가 함께 있다. 위 트리는 실제 파일을 기준으로 필요한 경계를 추렸다.

## 3. 요청 한 건의 경로

1. FastAPI의 `/v1/chat/completions`가 `user_api_key_auth`를 dependency로 실행한다. 사용자 키의 인증 문맥이 공급처 API 키와 구분된다.
2. `chat_completion`이 본문과 인증 문맥을 공통 요청 처리기로 전달한다.
3. 공통 처리기는 요청 정규화, pre-call hook, 정책과 로깅 문맥을 준비한다. 요청 처리 중 moderation hook과 추론 호출을 함께 관리하는 경로도 있다.
4. `route_request`가 모델 레지스트리와 요청 형식을 보고 Router 또는 직접 `litellm.<operation>` 호출을 고른다. 모든 요청이 같은 라우팅 분기를 거치는 것은 아니다.
5. 공개 `Router`는 `make_core`로 만든 `RustCore`에 연결된다.
6. Python 드라이버가 Rust 실행 단계와 Python 공급처 호출을 연결한다. 요청별 선택 예약과 사용량 읽기 문맥도 이곳에서 연다.
7. Rust가 후보 필터, 재시도와 fallback의 다음 동작을 정한다. 공급처 호출은 `PythonHost.invoke`를 통해 실제 `litellm` 함수로 돌아간다.
8. 응답·usage·오류가 코어와 프록시 처리기로 돌아가며, 스트림은 Python 래퍼가 계속 관리한다.

근거: [HTTP 진입](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/proxy/proxy_server.py#L13537), [공통 처리](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/proxy/common_request_processing.py#L3683), [분기](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/proxy/route_llm_request.py#L833), [직접 호출 조건](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/proxy/route_llm_request.py#L914), [드라이버](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_rust/core.py#L2970), [Host 호출](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_python_host.py#L108).

## 4. 모델·배포·계정은 다른 단위다

Router는 사용자가 요청하는 모델 그룹과 실제 배포를 구분한다. 한 그룹에 여러 공급처 또는 자격증명을 가진 배포를 묶고, 요청 조건과 상태를 반영해 후보를 고른다. 그룹명 변경과 공급처별 실제 모델명 변환은 구분해야 한다.

Rust `Candidates`에는 권한 필터를 거친 전체 후보와 건강한 후보가 따로 있다. 선택 단계는 팀, web search, cooldown, 차단, plugin, pre-call 조건, tag, order, retry 제외 대상을 반영한다. `simple-shuffle`은 Rust 구현이며 다른 전략은 Host 선택으로 넘기는 경계가 존재한다.

근거: [Candidates](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/rust/crates/router-core/src/select.rs#L68), [선택 단계](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/rust/crates/router-core/src/select.rs#L1).

## 5. 재시도와 fallback

같은 그룹의 다른 배포를 다시 시도하는 것과 다른 모델 그룹으로 넘어가는 것은 별도 동작이다. Rust `call.rs`가 시도와 fallback 체인을 관리한다. 오류 종류, 요청별 설정, order와 기본 fallback이 실제 경로를 바꾼다.

다른 그룹으로 넘어갈 때 Python 경계의 `allow_fallback`은 접근 권한, 예산, rate limit을 검사한다. 실패하면 `NOT_AUTHORIZED`, `OVER_BUDGET`, `RATE_LIMITED`로 건너뛴 사유를 기록한다. ‘원래 모델이 실패했으니 아무 모델이나 사용한다’는 구현이 아니다.

근거: [Rust fallback 승인](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/rust/crates/router-core/src/call.rs#L523), [권한·예산·한도 검사](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_rust/core.py#L5465).

프리웨이가 참고할 부분은 전환 실패의 이유를 구조화하는 방식이다. 무료 경로가 없을 때 유료 모델을 자동 선택할지는 명시적인 사용자 정책이어야 한다. 예산 검사만으로 ‘반드시 무료’라는 의미가 생기지 않는다.

## 6. 사용량·예약·무료 판정의 경계

`PythonHost.routing_cache`는 메모리와 선택적인 Redis 계층을 구성한다. `RouterUsage`는 deployment별 분 단위 요청·토큰 카운터와 로컬/공유 읽기 인터페이스를 제공한다. 이것은 라우터가 관측·집계한 사용량이다. 각 공급처 계정의 실제 남은 무료 프로모션 권한과 같은 데이터가 아니다.

`least_busy`에는 선택 예약과 소유자, 예약 해제를 위한 문맥이 있다. `_drive`는 실제 호출에서 해당 문맥을 연다. 반면 단순 배포 조회의 예약 정책은 다르다. 이 예약은 부하 선택을 위한 것이므로, 공급처 무료 토큰을 선점하고 과금을 방지하는 원자적 예약이라고 부르면 안 된다.

`lowest_cost`는 deployment 가격 정보를 읽는 별도의 선택 전략이다. 가장 저렴한 후보는 가격이 0인 후보라는 뜻이 아니다. 공급처 프로모션 종료와 구독 권한을 검증하는 절차도 가격 정렬 그 자체에 포함되지 않는다.

근거: [메모리·Redis](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_python_host.py#L157), [카운터 계약](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/redis/stores/router_usage/__init__.py#L46), [예약 문맥](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_strategy/least_busy.py#L26), [실제 호출의 예약](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_rust/core.py#L2982), [가격 조회](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_strategy/lowest_cost.py#L24), [비용 전략](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_strategy/lowest_cost.py#L208).

따라서 프리웨이의 검증 기준은 ‘cost routing 지원 여부’가 아니라, 사용자 소유 경로의 실제 무료 권한·인증·남은 할당량·허용 기능을 얼마나 적은 설정으로 유지하는지여야 한다.

## 7. 스트리밍: 다른 두 프로젝트보다 세밀한 경계

첫 chunk를 읽다가 실패하면 아직 응답을 확정하지 않은 시도로 처리할 수 있다. 이후라도 사용자에게 내용이 나가기 전이면 코어에 재시도 또는 fallback 정책을 묻는다. Python 래퍼는 실제 iterator와 프레임을 보유하고 Rust는 정책을 정한다.

여기서 분석상 중요한 예외가 있다. ‘일부 출력 후에는 무조건 전환 불가’라고 LiteLLM까지 묶어 설명하면 틀린다. 비동기 chat 스트림에는 `enable_mid_stream_fallback_continuation`으로 여는 제한적 이어쓰기 경로가 있다. 이미 생성된 일반 텍스트를 이어쓰기 요청으로 만들어 다음 응답과 접합한다. 생성된 내용, 지원 배포, 요청 제약, content policy 등에 대한 추가 검사를 통과해야 한다.

Responses 스트림에도 이어쓰기 요청 구성 코드가 있으며, 이미 tool call을 시작했거나 출력 제약이 있는 경우 등에는 거절한다. 동기 chat 래퍼는 출력 후 이어쓰기를 하지 않는다고 명시한다. 이것은 도구 실행 상태·파일 변경·코딩 에이전트 작업 전체를 다른 공급처로 이관하는 기능이 아니다.

근거: [래퍼 경계](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_rust/invoke/streaming.py#L1), [Rust stream 정책](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/rust/crates/router-core/src/call/stream.rs#L151), [비동기 chat 이어쓰기 허용 조건](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_rust/invoke/streaming.py#L666), [출력 후 분기](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_rust/invoke/streaming.py#L983), [동기 경계](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_rust/invoke/streaming.py#L1048), [Responses 제약](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_rust/invoke/streaming.py#L1752).

## 8. 로컬 모델과 OAuth 경로

Ollama 어댑터는 기본 `http://localhost:11434/api/chat`에 HTTP 요청을 보낸다. 게이트웨이가 모델 가중치를 직접 로딩한다는 뜻이 아니다. 사용자는 별도 로컬 추론 서버를 실행한다.

ChatGPT Responses 어댑터에는 authenticator에서 access token을 얻고 account 정보를 헤더에 넣는 코드가 있다. 기본 endpoint는 `https://chatgpt.com/backend-api/codex`다. 이 경로 역시 Codex 실행 파일을 subprocess로 호출하는 구조와 다르다. OAuth 지원을 ‘공식 CLI 실행 지원’으로 바꾸어 적으면 안 된다.

근거: [Ollama URL](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/llms/ollama/chat/transformation.py#L264), [ChatGPT 토큰](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/llms/chatgpt/responses/transformation.py#L67), [ChatGPT endpoint](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/llms/chatgpt/common_utils.py#L36).

## 9. 상태 변경과 재사용할 설계

Rust 코어는 registry와 settings를 `RouterStateSnapshot`으로 묶는다. `Core`는 `ArcSwap` 스냅샷, writer mutex, retired 상태를 보유한다. 요청이 읽는 설정과 동적 갱신의 경계를 명시적으로 설계한 사례다.

공개 코어 계약도 `call`, `registry`, `settings`, `health`, `select`, `apply`, `observe`, `register_plugin`, `retire`로 분리한다. 프리웨이가 언어를 나누거나 UI와 daemon을 분리한다면, 이 정도로 계약을 분명히 해야 한다. 다만 이 대형 구현 전체를 가져오는 것이 작은 개인용 제품에 적절하다는 결론은 아니다.

근거: [스냅샷과 Core](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/rust/crates/router-core/src/lib.rs#L143), [공개 계약](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/litellm/router_core.py#L27).

## 10. 라이선스와 적용 판단

루트 LICENSE는 `litellm/enterprise/`를 별도 라이선스 대상으로 분리하고 나머지를 MIT로 명시한다. 따라서 ‘LiteLLM의 모든 코드를 MIT로 가져올 수 있다’고 쓰면 틀린다. 재사용 파일의 위치와 고지 의무를 실제 LICENSE로 확인해야 한다.

근거: [루트 LICENSE](https://github.com/BerriAI/litellm/blob/d034fb681ca296a0f082a224043eda62a2ce82d4/LICENSE#L1). 이는 저장소의 라이선스 문구 대조이며 법률 자문은 아니다.

## 11. 이번 실행 검증의 범위

Rust `litellm-router-core`의 기존 `call_loop` 시험 111개가 통과했다. 시험은 ScriptedHost를 사용하며 실제 모델 API나 개인 자격증명을 사용하지 않는다. 같은 그룹 재시도, fallback 거절, cooldown, 요청별 fallback, 스트림 관련 코어 결정을 포함한다.

```bash
/home/rst010/.cargo/bin/cargo +stable test --locked --offline --manifest-path litellm/litellm/rust/Cargo.toml -p litellm-router-core --test call_loop --target-dir /tmp/freeway-research-build/litellm
```

실제 출력: `111 passed; 0 failed; 0 ignored`. Python 프록시 전체, Redis·DB 연동, 위에서 읽은 모든 스트림 이어쓰기 분기를 실행 검증한 결과는 아니다. 추가 실행은 사용자의 범위 지적 후 중단했다.

## 12. 프리웨이에 주는 결론

- 모델 그룹·부하 분산·오류 전환·예산 검사 자체를 새 기능으로 내세우지 않는다.
- 사용자 무료 경로의 준비 상태를 검증하고 유지하는 사용자 흐름을 별도 평가한다.
- 선택 예약, 사용량 집계, 실제 공급처 잔여 권한을 서로 다른 데이터로 설계한다.
- 공식 CLI subprocess, OAuth HTTP, 일반 API key HTTP를 어댑터 유형에서 구분한다.
- 부분 출력 후 복구를 만들 경우 텍스트 이어쓰기와 tool/agent 상태 이관을 구분한다.
- LiteLLM을 기반으로 삼을지는 필요한 Python·Rust·proxy 의존성 범위를 정한 뒤 판단한다.
