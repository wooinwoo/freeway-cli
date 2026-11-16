# 출처와 지식창고 갱신 규칙

## 출처의 신뢰 범위

기준일은 2026년 11월 16일 KST다. 공급처 공식 문서와 공식 API, 로컬 코드, 실제 실행 결과를 우선한다. 검색 결과 요약은 원문을 찾는 용도로만 사용한다. 저장소 `research.json`의 2026년 11월 15일 조사는 초기 후보 목록이며 이번 실제 호출 결과와 구분한다.

아래에서 현재 확인은 해당 원문 또는 공개 데이터를 이번 세션에서 읽었다는 뜻이다. 계정 가입이나 추론 성공을 의미하지 않는다. 접근 실패와 기존 조사 자료는 따로 표시한다.

## OpenRouter와 Ori

| ID | 원문 | 사용 범위 |
| --- | --- | --- |
| OR 001 | [모델 API](https://openrouter.ai/api/v1/models) | 현재 모델 ID·가격·기능 메타데이터 |
| OR 002 | [공급처 API](https://openrouter.ai/api/v1/providers) | 현재 공개 공급처 목록 |
| OR 003 | [모델 전환](https://openrouter.ai/docs/guides/routing/model-fallbacks) | 대체 모델과 가격 조건 |
| OR 004 | [공급처 선택](https://openrouter.ai/docs/guides/routing/provider-selection) | 순서·허용 공급처·fallback·최대 가격 |
| OR 005 | [무료 라우터](https://openrouter.ai/docs/guides/routing/routers/free-router) | 기능 필터와 무료 모델 자동 선택 |
| OR 006 | [BYOK](https://openrouter.ai/docs/guides/overview/auth/byok) | 키 우선순위·공유 용량·엔드포인트 경계 |
| OR 007 | [한도](https://openrouter.ai/docs/api_reference/limits) | 계정·키·무료 요청 제한과 조회 |
| OR 008 | [스트리밍](https://openrouter.ai/docs/api_reference/streaming) | SSE와 중간 오류 처리 |
| OR 009 | [Ori 소개](https://openrouter.ai/docs/guides/ori/overview.md) | CLI 역할과 기본 모델 선택 |
| OR 010 | [Ori 설정](https://openrouter.ai/docs/guides/ori/configuration) | 설정 우선순위와 인증 저장 범위 |
| OR 011 | [Ori Harness 발표](https://openrouter.ai/blog/announcements/ori-harness/) | 기존 CLI 연동의 목적 |
| OR 012 | [개발 도구](https://openrouter.ai/developers) | DevTools CLI와 실제 에이전트 도구 구분 |
| OR 013 | [Jev 설명](https://openrouter.ai/blog/insights/what-is-jev/) | 판단 API와 별도 usage 형식. 실제 비용의 증거는 계정 조회 |
| OR 014 | [DeepSeek 목록](https://openrouter.ai/deepseek) | 모델 계열의 공식 소개 |
| OR 015 | [Presets](https://openrouter.ai/docs/guides/features/presets) | 설정을 저장하는 기존 기능 |
| OR 016 | [구조화 출력](https://openrouter.ai/docs/guides/features/structured-outputs) | 기능 호환성의 추가 확인 지점 |
| OR 017 | [Auto 라우터](https://openrouter.ai/docs/guides/routing/routers/auto-router) | 일반 Auto와 무료 전용 라우터의 구분 |

## 대표 무료 경로

| ID | 원문 | 이번 확인 수준 |
| --- | --- | --- |
| P 001 | [OpenCode 모델과 무료 조건](https://opencode.ai/docs/console/models) | 현재 문서 확인 |
| P 002 | [OpenCode 공급처](https://opencode.ai/docs/providers/) | 현재 문서 확인 |
| P 003 | [OpenCode V1 서버](https://opencode.ai/v1/docs/server/) | 기존 조사 참조. 설치 버전은 별도 확인 |
| P 004 | [OpenCode V1 SDK](https://opencode.ai/v1/docs/sdk/) | 기존 조사 참조 |
| P 005 | [Cline 무료 모델](https://docs.cline.bot/getting-started/free-models) | 현재 문서 확인. API 무료 제외 명시 |
| P 006 | [Cline CLI](https://docs.cline.bot/usage/cli-overview) | 현재 headless·JSON 계약 확인 |
| P 007 | [Kilo 카탈로그](https://api.kilo.ai/api/gateway/models) | 현재 공개 데이터 조회 |
| P 008 | [Kilo 사용량과 과금](https://kilo.ai/docs/gateway/usage-and-billing) | 현재 문서 확인 |
| P 009 | [Kilo 인증](https://kilo.ai/docs/gateway/authentication) | 현재 문서 확인 |
| P 010 | [Requesty 무료 모델](https://www.requesty.ai/models/free) | 현재 무료 할당과 모델 목록 확인 |
| P 011 | [Requesty Muse Glimmer](https://www.requesty.ai/model/meta/muse-glimmer-30b) | 현재 0원 경로와 API 예시 확인 |
| P 012 | [NVIDIA API 시작](https://docs.api.nvidia.com/nim/docs/api-quickstart) | 기존 조사 참조. 현재 계정 호출 미검증 |
| P 013 | [NVIDIA FAQ](https://build.nvidia.com/api/runtime/content/help-center-faq.yaml) | 기존 조사 참조. 이번 웹 도구 접근 실패 |
| P 014 | [Z.ai 가격](https://docs.z.ai/guides/overview/pricing) | 현재 무료 GLM Flash 모델 확인 |
| P 015 | [Vikasit](https://vikasit.ai/) | 현재 일일 무료 토큰 공식 안내 확인 |
| P 016 | [Groq 한도](https://console.groq.com/docs/rate-limits) | 현재 모델별 표 확인 |
| P 017 | [Gemini 가격](https://aistudio.google.com/docs/pricing) | 현재 무료·유료 구분 확인 |
| P 018 | [Cloudflare 가격](https://developers.cloudflare.com/workers-ai/platform/pricing/) | 현재 Neurons 할당과 유료 모델 조건 확인 |
| P 019 | [Novita 가격](https://novita.ai/pricing) | 현재 Apodex 무료 입력·출력 확인 |

## 추가 후보

| ID | 원문 | 사용 범위와 상태 |
| --- | --- | --- |
| C 001 | [Qoder 일일 Credits](https://docs.qoder.com/events/100credits) | 현재 이벤트 문서 확인 |
| C 002 | [Qoder 스크립트 실행](https://docs.qoder.com/cli/run-in-scripts) | 기존 조사 참조 |
| C 003 | [CodeBuddy 가격](https://www.codebuddy.ai/pricing/) | 이번 본문을 읽지 못함 |
| C 004 | [CodeBuddy headless](https://www.codebuddy.ai/docs/cli/headless) | 기존 조사 참조 |
| C 005 | [Antigravity 가격](https://antigravity.google/pricing) | 현재 개인 무료·주간 제한 안내 확인 |
| C 006 | [Antigravity headless](https://antigravity.google/docs/cli/headless/) | 기존 조사 참조 |
| C 007 | [Copilot](https://github.com/features/copilot/) | 현재 제품 페이지 확인. Free SDK 권한은 미확정 |
| C 008 | [Copilot SDK](https://github.com/github/copilot-sdk) | 기존 조사 참조 |
| C 009 | [Kiro 가격](https://kiro.dev/pricing/) | 현재 월 50 Credits 안내 확인 |
| C 010 | [Kiro headless](https://kiro.dev/docs/cli/headless/) | 기존 조사 참조 |
| C 011 | [ChatGPT 가격](https://learn.chatgpt.com/docs/pricing) | 현재 공식 페이지 열람. Free Codex 표면별 권한은 미검증 |
| C 012 | [Codex 플랫폼](https://developers.openai.com/blog/codex-as-a-platform) | 기존 조사 참조 |
| C 013 | [AIHubMix 무료 설정](https://aihubmix.com/call/free_quota_config) | 현재 공개 JSON 조회 |
| C 014 | [AIHubMix 개발자](https://aihubmix.com/developers) | 기존 조사 참조 |
| C 015 | [SimpleLLM 한도](https://simplellm.eu/docs/limits.html) | 현재 문서 확인. 실제 계정 한도 미검증 |
| C 016 | [SimpleLLM 모델](https://simplellm.eu/docs/models.html) | 기존 조사 참조 |
| C 017 | [LLM7 약관](https://github.com/chigwell/llm7.io/blob/main/TERMS.md) | 원문 위치 확인. 실제 적용 조건 재확인 필요 |
| C 018 | [SenseNova Token Plan](https://www.sensenova.cn/token-plan) | 이번 접근 실패 |
| C 019 | [Clixad 무료 모델](https://clixad.io/free-ai-models-for-coding/) | 현재 DeepSeek·할당·추정 turn 조건 확인 |
| C 020 | [Clixad CLI](https://github.com/Clixad/cli) | 기존 조사 참조. headless 계약 미확정 |
| C 021 | [Freebuff 플랜](https://freebuff.com/plans) | 현재 무료 플랜의 DeepSeek 조건 확인 |
| C 022 | [Freebuff 약관](https://freebuff.com/terms-of-service) | 현재 외부 중계·래퍼·자동화 제한 확인 |
| C 023 | [Pollinations FAQ](https://raw.githubusercontent.com/pollinations/pollinations/main/enter.pollinations.ai/POLLEN_FAQ.md) | 현재 Quest 보상형 크레딧 구조 확인 |
| C 024 | [Mistral 가격](https://mistral.ai/pricing/) | 현재 제한된 무료 코딩 세션 안내 확인 |
| C 025 | [Mistral Vibe CLI](https://docs.mistral.ai/vibe/code/cli/work-with-cli) | 기존 조사 참조 |
| C 026 | [Cursor 가격](https://cursor.com/pricing) | 현재 무료 Agent·Chat 제한 확인 |
| C 027 | [Cursor CLI](https://prod.cursor.com/docs/cli/overview) | 기존 조사 참조 |
| C 028 | [CoStrict 사용량](https://docs.costrict.ai/plugin/billing/usage) | 이번 접근 실패 |
| C 029 | [CoStrict headless](https://docs.costrict.ai/en/csc/automation/headless) | 기존 조사 참조 |
| C 030 | [Jules 한도](https://jules.google/docs/usage-limits) | 현재 원문 위치 확인. 상세 수치는 재검증 항목 |
| C 031 | [Jules API](https://jules.google/docs/api/reference/) | 기존 조사 참조 |
| C 032 | [Devin 가격](https://devin.ai/pricing) | 현재 무료 제품 존재 확인. 무료 API 권한 미확정 |

## 로컬 코드 근거

상대 경로는 이 폴더를 기준으로 한다. 코드 줄 번호는 커밋에 따라 달라지므로 기준 커밋과 함수 이름을 함께 기록한다.

| 근거 | 파일과 심볼 |
| --- | --- |
| 패키지·검증 명령 | [package.json](../../POC/package.json) |
| 제품의 기존 설명 | [README](../../README.md) |
| 초기 30개 후보 | [research.json](../../POC/research.json) |
| 공급처와 무료 판별 | [catalog.mjs](../../POC/src/catalog.mjs), `definitions`, `freeModel`, `normalizeModel` |
| 경로 선택과 fallback | [engine.mjs](../../POC/src/engine.mjs), `candidates`, `complete` |
| 한도와 예약 | [store.mjs](../../POC/src/store.mjs), `capacity`, `reserve`, `record` |
| OpenCode 텍스트 브리지 | [opencode.mjs](../../POC/src/opencode.mjs), `deniedTools`, `complete` |
| API 전송 | [server.mjs](../../POC/server.mjs), `createGateway` |
| 동시 CLI의 서버 시작 | [client.mjs](../../POC/src/client.mjs), `Client.connect` |
| 프로토콜 변환 | [adapters.mjs](../../POC/src/adapters.mjs), `allowed`, `openAICompletion` |
| 모델 정체성 | [model-groups.mjs](../../POC/src/model-groups.mjs), `modelIdentity` |
| 텍스트 위임 | [mcp.mjs](../../POC/src/mcp.mjs), `freeway_delegate` |

## 증거를 추가하는 형식

새 실험은 날짜, 경로, 계정 권한의 종류, 명령 또는 요청의 비밀 없는 형태, 상태 코드, 실제 모델, 비용, 사용량의 근거, 성공·실패·미완료 판정을 기록한다. 로그인 토큰이나 개인 계정 식별자를 기록하지 않는다.

`evidence/2026-11-16-baseline.json`은 이번 확인의 요약이다. 임시 진단 출력 전체를 복사하지 않고 제품 판단에 필요한 필드만 남긴다. 다른 사람이 같은 계정 비밀 없이 검증 범위를 이해할 수 있어야 한다.

## 갱신 규칙

무료 모델과 프로모션은 출시 전에 다시 확인한다. 기존 조사를 현재 사실로 올리려면 원문 재확인과 필요한 계정 시험을 수행한다. 수치가 바뀌면 기존 수치의 적용 시점을 보존하고 현재 적용 값을 갱신한다.

새로운 반례가 있으면 개요, 공급처 등록부, 요구사항, 관련 ADR과 시험 기록을 함께 수정한다. 출처를 늘리는 것보다 결정에 영향을 주는 주장이 어디에 연결되는지 유지하는 것을 우선한다.
