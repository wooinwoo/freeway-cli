# 무료 공급처와 연동 경로 등록부

## 조사 원칙

모든 후보는 무료 조건, 대상 모델, 호출 인터페이스, 인증, 한도, 외부 연동 가능성을 따로 기록한다. 이 등록부의 초기 후보 30개는 저장소 `research.json`에서 가져왔다. 현재 공식 자료를 다시 읽은 항목과 이전 조사만 남은 항목을 아래에서 구분한다.

등록부에 있다는 이유만으로 기본 라우팅에 활성화하지 않는다. 현재 계정의 권한과 최소 호출을 확인한 경로만 활성 후보로 승격한다. 가입·카드 등록·유료 플랜·지역 조건이 붙은 무료 할당은 무조건 무료 API와 분리한다.

## 우선 조사 결과

| 경로 | 대상과 무료 조건 | 인터페이스 | 이번 확인 수준 | 다음 검증 |
| --- | --- | --- | --- | --- |
| OpenCode | DeepSeek V4.1 Flash Free 등 순환 무료 모델 | 공식 CLI, headless 서버 | 공식 문서와 로컬 CLI 목록 확인 | 실제 호출, 무료 한도 초과, 기능 보존 |
| Cline | 계정에 제공되는 기간 한정 무료 모델 | 공식 CLI와 IDE | 무료 API 제외 조건, 로컬 CLI와 설정 모델 확인 | CLI 호출과 모델별 무료 표시, 쿼터 |
| Requesty | Muse Glimmer 30B 등 지정 무료 모델, 일 200회 안내 | OpenAI 호환 API | 현재 공식 모델·무료 페이지 확인 | API 키 연결 후 모델·추론·사용량 확인 |
| NVIDIA NIM | 무료 프로토타이핑 경로 | 공식 모델 API | 기존 조사 및 Requesty의 NVIDIA 경로 확인 | 계정 키, 현재 DeepSeek·Muse 목록, 계정 한도 |
| Kilo | 0원 모델, IP당 시간 200회 안내 | OpenAI 호환 게이트웨이 | 실시간 모델 목록과 공식 조건 확인 | 무인증 무료 모델 호출, 429와 비용 |
| Z.ai | 지정 GLM Flash 모델 | 공식 API | 현재 가격 페이지 확인 | 최신 무료 모델 ID와 계정 호출 |
| OpenRouter | 지정 무료 모델 | OpenAI 호환 API | 무료 선택과 경로 대체 실제 호출 성공 | 실제 429, BYOK 무료 권한 |

OpenCode 무료 모델은 외부 클라이언트에서 직접 무료 API를 쓸 수 있다고 가정하면 안 된다. Cline은 무료 사용을 API에서 지원하지 않는다고 명시한다. 두 경우 모두 공식 CLI 또는 서버 사용 가능성과 Freeway의 어댑터 기능을 검증해야 한다. [OpenCode](https://opencode.ai/docs/console/models), [Cline](https://docs.cline.bot/getting-started/free-models)

## DeepSeek 경로 비교

| 공급처 | 확인된 모델 또는 계열 | 무료 여부 | 해석 |
| --- | --- | --- | --- |
| OpenRouter | `deepseek/deepseek-v4.1-flash-1017` 등 | 이번 조회 목록에서는 유료 | OpenRouter만으로 무료 DeepSeek를 확보했다고 할 수 없다 |
| OpenCode | `opencode/deepseek-v4.1-flash-free` | 공식 기간 한정 무료 | 로컬 CLI가 해당 ID를 표시한다 |
| Cline | 로컬 설정의 `cline-free/deepseek-v4.1-flash` | 무료 경로를 가리키는 설정 | 설정 ID만으로 현재 무료 권한과 호출 성공을 증명하지 않는다 |
| NVIDIA | DeepSeek 계열 제공 가능성을 조사 대상으로 유지 | 무료 프로토타입 계정 조건 확인 필요 | 현재 키와 모델 목록을 확인해야 한다 |
| Clixad | DeepSeek V4 Flash | 일일 공유 무료 할당 안내 | 작업용 CLI 경로이며 일반 API와 동일하게 취급하지 않는다 |
| Freebuff | DeepSeek V4.1 Flash | 개인용 무료 플랜 안내 | 외부 중계·래퍼·자동화 제한 때문에 기본 연동에서 제외한다 |
| Kilo | 조회된 DeepSeek 계열 | 이번 목록에서는 유료 | Kilo에 무료 모델이 있다고 DeepSeek도 무료인 것은 아니다 |

DeepSeek의 버전이 다르면 동일 모델 공급 경로로 병합하지 않는다. V4 Flash, V4.1 Flash, 1017 snapshot은 별도 식별 근거를 보존한다. [OpenCode 무료 모델](https://opencode.ai/docs/console/models), [Clixad](https://clixad.io/free-ai-models-for-coding/), [Freebuff 플랜](https://freebuff.com/plans)

## Muse 경로 비교

Muse는 모델 계열 이름이다. Glimmer 30B, Ember, Spark, Wonder는 서로 다른 모델로 등록한다. Muse Code라는 코딩 도구도 같은 이름의 모델과 구분한다.

| 공급처 | 모델 | 관측 또는 공식 안내 |
| --- | --- | --- |
| Requesty | `muse-glimmer-30b` | NVIDIA 경로의 입력·출력 무료 가격과 API 호출 예시를 제공한다 |
| Kilo | `meta/muse-glimmer-30b` | 이번 카탈로그에서는 입력·출력 단가가 0이 아니었다 |
| OpenCode | Muse Ember, Wonder, Spark | 공식 모델 표에는 유료 모델로 표시되며, 이번 로컬 무료 목록에는 없었다 |
| NVIDIA | Glimmer 제공 경로 | Requesty의 안내와 직접 NVIDIA 계정 권한은 별도 검증해야 한다 |

Requesty의 무료 Glimmer를 찾았다고 사용자가 원하는 Muse Spark가 무료로 확보됐다고 보고하지 않는다. 선택 화면에서 계열과 변형을 모두 노출한다. [Requesty Muse Glimmer](https://www.requesty.ai/model/meta/muse-glimmer-30b)

## 30개 후보 전체 목록

| 번호 | 후보 | 무료 자원과 제한 | 연동 방식 | 현재 판단 |
| --- | --- | --- | --- | --- |
| 01 | Kilo | 지정 0원 모델, IP당 시간 200회 | 직접 API | 현재 목록 확인. DeepSeek와 Muse 유료 경로 구분 필요 |
| 02 | NVIDIA NIM | 프로토타입 무료 권한, 모델·계정 제한 | 직접 API | 우선 후보. API 키가 없어 추론은 미검증 |
| 03 | Z.ai | 지정 GLM Flash의 무료 가격 | 직접 API | 현재 가격 확인. 모든 GLM을 무료로 취급하지 않는다 |
| 04 | Vikasit | Nova의 일 200만 토큰 안내 | 직접 API | 현재 공식 안내 확인. 실제 계정 권한 미검증 |
| 05 | Requesty | 지정 무료 모델 일 200회 안내 | 직접 API | Muse Glimmer가 핵심 후보. 키 연결 필요 |
| 06 | GroqCloud | 모델별 요청·토큰 무료 한도 | 직접 API | 공식 한도 문서 확인. 실제 계정 한도가 우선 |
| 07 | Gemini API | 일부 모델 무료 입력·출력 | 직접 API | 공식 무료 요금제 확인. 프로젝트별 한도와 모델 권한 확인 필요 |
| 08 | OpenRouter | 지정 무료 모델과 계정 일일 제한 | 직접 API | 실제 호출 확인. 외부 CLI 무료 권한은 별도 |
| 09 | Cloudflare Workers AI | 일 10000 Neurons 무료 안내 | REST 또는 Workers | 가격 문서 확인. Neurons를 토큰으로 표시하지 않는다 |
| 10 | Novita | Apodex Mini 무료 후보 | 직접 API | 가격 페이지 확인. 계정 자금·상한·현재 모델 활성 여부 추가 확인 |
| 11 | OpenCode | 기간 한정 무료 모델 | 공식 CLI 또는 서버 | DeepSeek 무료 목록 확인. 어댑터 적합성 시험 우선 |
| 12 | Cline | 기간 한정 무료 모델·계정 쿼터 | 공식 CLI 또는 IDE | API 무료 제외. CLI 경로로만 검증을 진행한다 |
| 13 | Qoder | 데스크톱에서 일 100 Credits 수령 이벤트 | 공식 headless CLI | 이벤트 문서 확인. 수동 수령과 Credits 단위 보존 |
| 14 | CodeBuddy | 기존 조사상 월 무료 Credits와 활동 보상 | 공식 CLI | 현재 가격 페이지 본문을 읽지 못해 재확인 필요 |
| 15 | Antigravity | 개인 0원 플랜과 주간 제한 | 공식 headless CLI 후보 | 무료 플랜 확인. headless 권한과 모델 목록 별도 |
| 16 | GitHub Copilot | Free의 제한된 AI 사용 | 공식 CLI와 SDK 후보 | 무료 제품 존재와 SDK 권한을 별도로 확인해야 한다 |
| 17 | Kiro | Free 월 50 Credits 안내 | 공식 headless CLI 후보 | 가격 확인. API 키 인증이 Free에 적용되는지 미검증 |
| 18 | Codex | ChatGPT Free의 제한된 이용 후보 | 공식 CLI와 SDK 후보 | 무료 계정의 해당 표면 권한·자동화 가능 범위 추가 확인 |
| 19 | AIHubMix | 실시간 설정상 무료 할당과 미결제 체험 제한 | 직접 API | 공개 한도 JSON 확인. 결제 임계값과 모델 가중치 때문에 조건부 |
| 20 | SimpleLLM | 지정 무료 모델과 계정 예산 | 직접 API | 한도 문서 확인. 현재 모델과 인증 후 남은 한도 필요 |
| 21 | LLM7 | 기존 조사상 연구·교육용 토큰 및 요청 제한 | 직접 API | 원문 약관을 실제 용도와 대조한 후 채택 여부 결정 |
| 22 | SenseNova | 기존 조사상 0원 베타 | API 키·도구 연동 후보 | 현재 페이지 접근 실패. 확정 무료 경로로 등록하지 않는다 |
| 23 | Clixad | 일 60000 공유 Credits 안내 | 공식 CLI | 무료 DeepSeek 안내 확인. headless 계약과 자동화 범위 추가 확인 |
| 24 | Freebuff | 개인용 일일 무료 할당 | 자사 제품 | 외부 래퍼·중계·자동화 제한이 명시되어 초기 연동 제외 |
| 25 | Pollinations | Quest 수행으로 받는 Pollen | 직접 API | 현재 FAQ 확인. 고정 자동 갱신 무료 할당과 구분 |
| 26 | Mistral Vibe | 제한된 무료 코딩 세션 | 공식 CLI 후보 | 가격 확인. 무료 세션의 CLI 권한과 한도 추가 확인 |
| 27 | Cursor | Free의 제한된 Agent·Chat 요청 | 공식 CLI 후보 | 현재 가격 확인. 무료 CLI entitlement 추가 확인 |
| 28 | CoStrict | 기존 조사상 주간 Credits와 기본 사용량 | CLI·서버 후보 | 이번 문서 접근 실패. 활성 경로로 승격하지 않는다 |
| 29 | Jules | 제한된 무료 작업 수 | REST·작업용 CLI | 작업 서비스로 분류. 대화 추론 API와 분리 |
| 30 | Devin 및 Windsurf | Free의 제한된 에이전트 사용 | 제품 API·CLI 후보 | Free API 권한이 확인되지 않아 보류 |

각 행의 원문 URL은 [출처 목록](13-sources.md)에 모았다. 일회성 이벤트와 상시 무료 요금제를 같은 유형으로 표시하지 않는다. 이 표는 공급처별 기능 인증 결과가 아니라 채택 검증의 출발점이다.

## 활성화까지의 단계

```text
발견
  공식 무료 조건 확인
  인터페이스와 사용 조건 확인
  현재 계정 연결
  모델 목록 확인
  최소 추론 또는 작업 호출
  전체 비용과 기능 확인
  실패 및 한도 시험
  활성 경로 등록
```

중간에 확인되지 않은 조건은 `unknown`으로 남긴다. 로그인 성공을 무료 추론 성공으로, 모델 목록을 도구 호출 지원으로, CLI JSON 출력을 OpenAI 호환 API로 바꾸어 보고하지 않는다.

## 공급처별로 수집할 필드

`providerId`, `routeKind`, `canonicalModelId`, `upstreamModelId`, `authMethod`, `priceBasis`, `freeGrantType`, `quotaScope`, `quotaUnit`, `windowKind`, `resetRule`, `capabilities`, `automationEvidence`, `observedAt`, `expiresAt`, `sourceUrl`, `lastProbeResult`를 기록한다. 자격증명은 `credentialRef`로만 연결한다.

## 공유 한도의 주의점

다른 게이트웨이를 거쳐도 같은 NVIDIA 계정·IP·상위 무료 풀을 사용할 수 있다. 서로 다른 도메인이라는 이유만으로 독립 한도로 합산하지 않는다. 반대로 OpenCode와 OpenRouter가 같은 모델을 제공해도 무료 권한이 독립된 계정 정책일 수 있다. 실제 한도 범위와 출처를 조사해야 한다.

## 초기 채택 순서 제안

먼저 OpenCode DeepSeek 무료 경로, Cline 무료 경로, Requesty Muse 경로를 검증한다. 이 셋이 사용자의 대표 요구를 직접 검증한다. Kilo와 OpenRouter는 비교 기준과 일반 무료 모델 경로로 유지한다. 그 뒤 NVIDIA 직접 API와 Z.ai, Groq, Gemini처럼 공식 API가 명확한 후보를 연결한다. 나머지는 연동 검증 결과에 따라 추가하며, 30개를 한 번에 구현하는 것을 MVP 조건으로 삼지 않는다.
